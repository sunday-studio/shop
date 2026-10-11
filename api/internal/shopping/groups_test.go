package shopping

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"shop/internal/match"
	"shop/internal/stats"
	"shop/internal/store"
)

func product(t *testing.T, s *Service, name string) int64 {
	t.Helper()
	res, err := s.conn.Exec(`INSERT INTO products (name, created_at, updated_at) VALUES (?, 't', 't')`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// clock lets a test move time.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func withClock(s *Service) *clock {
	c := &clock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	s.now = c.now
	return c
}

func lines(texts ...string) []Input {
	out := make([]Input, len(texts))
	for i, t := range texts {
		out[i] = Input{Text: t}
	}
	return out
}

func ids(items []Item) []int64 {
	out := []int64{}
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestCreateGroupAtomicAndIdempotent(t *testing.T) {
	s, ctx := testService(t)
	milk := product(t, s, "Arla melk")
	in := GroupInput{
		ClientID: "g-1", Name: "  BBQ ", Items: []Input{
			{Text: "kippendijen", Qty: ptr(6.0)}, {ProductID: &milk}, {Text: "bier"}, {ProductID: &milk, Qty: ptr(2.0)},
		},
	}
	res, err := s.CreateGroup(ctx, in)
	if err != nil || !res.Created {
		t.Fatalf("create: %+v %v", res, err)
	}
	if res.Group.Name != "BBQ" || res.Group.Kind != KindChecklist || res.Group.Source != SourceUser {
		t.Errorf("group = %+v", res.Group)
	}
	// Four lines, the two milk lines share one item (one open item per product and group).
	if len(res.ItemIDs) != 4 || res.ItemIDs[1] != res.ItemIDs[3] || res.ItemIDs[0] == res.ItemIDs[1] {
		t.Fatalf("item ids = %v", res.ItemIDs)
	}
	if res.Group.ItemCount != 3 || len(res.Group.Items) != 3 {
		t.Errorf("items = %d / %d, want 3", res.Group.ItemCount, len(res.Group.Items))
	}
	if q := res.Group.Items[1].Qty.Float64; q != 3 { // 1 + 2
		t.Errorf("milk qty = %v, want 3", q)
	}

	// The phone never heard back and retries, three times: same group, same ids, nothing duplicated.
	for i := 0; i < 3; i++ {
		again, err := s.CreateGroup(ctx, in)
		if err != nil || again.Created || again.Group.ID != res.Group.ID || !slices.Equal(again.ItemIDs, res.ItemIDs) {
			t.Fatalf("retry %d: %+v %v", i, again, err)
		}
	}
	gs, _ := s.ListGroups(ctx, "", true, false)
	items, _ := s.List(ctx, true)
	if len(gs) != 1 || len(items) != 3 {
		t.Errorf("groups = %d items = %d, want 1 and 3", len(gs), len(items))
	}

	// The group was deleted on the other phone: the retry is told it is gone.
	if _, err := s.DeleteGroup(ctx, res.Group.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateGroup(ctx, in); !errors.Is(err, ErrGone) {
		t.Errorf("retry after delete = %v, want ErrGone", err)
	}
	// A client id belongs to one kind of thing.
	if _, _, err := s.Add(ctx, Input{Text: "x", ClientID: "g-1"}); !errors.Is(err, ErrConflict) {
		t.Errorf("item add with a group's client id = %v, want ErrConflict", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestCreateGroupRollsBack(t *testing.T) {
	s, ctx := testService(t)
	bad := int64(9999)
	cases := map[string]GroupInput{
		"unknown product": {Name: "A", Items: []Input{{Text: "ok"}, {ProductID: &bad}}},
		"empty line":      {Name: "A", Items: []Input{{Text: "ok"}, {Text: "  "}}},
		"no name":         {Name: " ", Items: lines("ok")},
		"bad kind":        {Name: "A", Kind: "list"},
		"bad source":      {Name: "A", Source: "bot"},
		"too many":        {Name: "A", Items: make([]Input, MaxGroupItems+1)},
	}
	for name, in := range cases {
		if _, err := s.CreateGroup(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	// Failing inside the transaction (the group is gone before the lines go in) leaves nothing behind either.
	if _, err := s.AddItems(ctx, 12345, lines("a"), ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("add to unknown group = %v", err)
	}
	gs, _ := s.ListGroups(ctx, "", true, false)
	items, _ := s.List(ctx, true)
	if len(gs) != 0 || len(items) != 0 {
		t.Errorf("left behind: %d groups, %d items", len(gs), len(items))
	}
}

func TestExternalRefIsIdempotentPerSource(t *testing.T) {
	s, ctx := testService(t)
	in := GroupInput{Name: "Jerk chicken", Source: SourceAssistant, ExternalRef: "jerk-1", Items: lines("kip", "thyme")}
	a, err := s.CreateGroup(ctx, in)
	if err != nil || !a.Created || a.Group.Source != SourceAssistant {
		t.Fatalf("%+v %v", a, err)
	}
	for _, it := range a.Group.Items {
		if it.Source != SourceAssistant {
			t.Errorf("item source = %q", it.Source)
		}
	}
	in.Items = lines("something else")
	b, err := s.CreateGroup(ctx, in)
	if err != nil || b.Created || b.Group.ID != a.Group.ID || len(b.Group.Items) != 2 {
		t.Errorf("repeat: %+v %v", b, err)
	}
	// Same ref from the user is a different group.
	in.Source = SourceUser
	c, err := s.CreateGroup(ctx, in)
	if err != nil || !c.Created || c.Group.ID == a.Group.ID {
		t.Errorf("user with same ref: %+v %v", c, err)
	}
}

func TestOneOpenItemPerProductAndGroup(t *testing.T) {
	s, ctx := testService(t)
	milk := product(t, s, "Arla melk")
	g1, _ := s.CreateGroup(ctx, GroupInput{Name: "A"})
	g2, _ := s.CreateGroup(ctx, GroupInput{Name: "B"})
	a, created, _ := s.Add(ctx, Input{ProductID: &milk})
	b, _, _ := s.Add(ctx, Input{ProductID: &milk, GroupID: &g1.Group.ID})
	c, _, _ := s.Add(ctx, Input{ProductID: &milk, GroupID: &g2.Group.ID})
	if !created || a.ID == b.ID || b.ID == c.ID {
		t.Fatal("the same product should be one item per group and the main list")
	}
	b2, created, _ := s.Add(ctx, Input{ProductID: &milk, GroupID: &g1.Group.ID, Qty: ptr(2.0)})
	if created || b2.ID != b.ID || b2.Qty.Float64 != 3 {
		t.Errorf("second add to the same group: created=%v qty=%v", created, b2.Qty.Float64)
	}
	// "Also in": each row knows where else the product is open.
	got, _ := s.GetGroup(ctx, g1.Group.ID)
	it := got.Items[0]
	slices.Sort(it.OtherGroupIDs)
	if !it.OnMainList || !slices.Equal(it.OtherGroupIDs, []int64{g2.Group.ID}) {
		t.Errorf("other groups = %v main=%v", it.OtherGroupIDs, it.OnMainList)
	}
	// A checked item is no longer open: the product can be added again.
	if _, err := s.SetChecked(ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	b3, created, _ := s.Add(ctx, Input{ProductID: &milk, GroupID: &g1.Group.ID})
	if !created || b3.ID == b.ID {
		t.Errorf("after checking, add should create a new item")
	}
	if _, _, err := s.Add(ctx, Input{Text: "x", GroupID: ptr(int64(777))}); !errors.Is(err, ErrNotFound) {
		t.Errorf("add to unknown group = %v", err)
	}
}

func TestMoveItem(t *testing.T) {
	s, ctx := testService(t)
	milk := product(t, s, "Arla melk")
	g1, _ := s.CreateGroup(ctx, GroupInput{Name: "A"})
	g2, _ := s.CreateGroup(ctx, GroupInput{Name: "B", Items: []Input{{ProductID: &milk, Qty: ptr(2.0)}}})
	tpl, _ := s.CreateGroup(ctx, GroupInput{Name: "T", Kind: KindTemplate, Items: lines("x")})
	it, _, _ := s.Add(ctx, Input{Text: "brood"})

	moved, err := s.MoveItem(ctx, it.ID, &g1.Group.ID)
	if err != nil || moved.GroupID.Int64 != g1.Group.ID || moved.ID != it.ID {
		t.Fatalf("move: %+v %v", moved, err)
	}
	if again, err := s.MoveItem(ctx, it.ID, &g1.Group.ID); err != nil || again.ID != it.ID {
		t.Errorf("moving to the same place: %v", err)
	}
	back, err := s.MoveItem(ctx, it.ID, nil)
	if err != nil || back.GroupID.Valid {
		t.Errorf("move to main: %+v %v", back, err)
	}

	// Moving a product into a group that already has it open adds the quantities.
	m, _, _ := s.Add(ctx, Input{ProductID: &milk, Qty: ptr(1.0)})
	merged, err := s.MoveItem(ctx, m.ID, &g2.Group.ID)
	if err != nil || merged.Qty.Float64 != 3 || merged.ID == m.ID {
		t.Errorf("merge: %+v %v", merged, err)
	}
	if _, err := s.get(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the moved item should be gone after a merge")
	}

	if _, err := s.MoveItem(ctx, it.ID, &tpl.Group.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("into a template = %v", err)
	}
	if _, err := s.MoveItem(ctx, tpl.Group.Items[0].ID, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("out of a template = %v", err)
	}
	if _, err := s.MoveItem(ctx, it.ID, ptr(int64(555))); !errors.Is(err, ErrNotFound) {
		t.Errorf("to unknown group = %v", err)
	}
	if _, err := s.MoveItem(ctx, 555, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown item = %v", err)
	}
}

func TestUpdateGroupArchiveAndOrder(t *testing.T) {
	s, ctx := testService(t)
	a, _ := s.CreateGroup(ctx, GroupInput{Name: "A", Items: lines("x")})
	b, _ := s.CreateGroup(ctx, GroupInput{Name: "B"})
	c, _ := s.CreateGroup(ctx, GroupInput{Name: "C"})
	if !(a.Group.Sort < b.Group.Sort && b.Group.Sort < c.Group.Sort) {
		t.Fatalf("new groups should go last: %d %d %d", a.Group.Sort, b.Group.Sort, c.Group.Sort)
	}
	g, err := s.UpdateGroup(ctx, c.Group.ID, GroupPatch{Name: ptr("C2"), Note: ptr("for friday"), Sort: ptr(int64(0))})
	if err != nil || g.Name != "C2" || g.Note.String != "for friday" {
		t.Fatalf("%+v %v", g, err)
	}
	list, _ := s.ListGroups(ctx, "", false, false)
	if list[0].ID != c.Group.ID {
		t.Errorf("order after sort=0: first is %d", list[0].ID)
	}
	g, _ = s.UpdateGroup(ctx, a.Group.ID, GroupPatch{Archived: ptr(true)})
	stamp := g.ArchivedAt.String
	if !g.ArchivedAt.Valid || len(g.Items) != 1 {
		t.Fatalf("archive: %+v", g)
	}
	if again, _ := s.UpdateGroup(ctx, a.Group.ID, GroupPatch{Archived: ptr(true)}); again.ArchivedAt.String != stamp {
		t.Errorf("archiving twice should keep the first time")
	}
	list, _ = s.ListGroups(ctx, "", false, false)
	if len(list) != 2 {
		t.Errorf("archived group should be hidden, got %d", len(list))
	}
	list, _ = s.ListGroups(ctx, "", true, false)
	if len(list) != 3 || list[2].ID != a.Group.ID {
		t.Errorf("archived last: %v", list)
	}
	g, _ = s.UpdateGroup(ctx, a.Group.ID, GroupPatch{Archived: ptr(false), Note: ptr("")})
	if g.ArchivedAt.Valid || g.Note.Valid {
		t.Errorf("unarchive: %+v", g)
	}
	if _, err := s.UpdateGroup(ctx, a.Group.ID, GroupPatch{Name: ptr(" ")}); !errors.Is(err, ErrInvalid) {
		t.Errorf("blank name = %v", err)
	}
	if _, err := s.UpdateGroup(ctx, 999, GroupPatch{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown = %v", err)
	}
}

func TestDeleteGroupKeepAndRestore(t *testing.T) {
	s, ctx := testService(t)
	c := withClock(s)
	res, _ := s.CreateGroup(ctx, GroupInput{Name: "BBQ", Items: lines("kip", "bier", "brood")})
	gid := res.Group.ID
	if _, err := s.SetChecked(ctx, res.ItemIDs[2], true); err != nil {
		t.Fatal(err)
	}

	del, err := s.DeleteGroup(ctx, gid, true)
	if err != nil || len(del.Kept) != 3 || len(del.DeletedItemIDs) != 0 || !del.RestoreUntil.After(c.now()) {
		t.Fatalf("keep: %+v %v", del, err)
	}
	for _, it := range del.Kept {
		if it.GroupID.Valid {
			t.Errorf("kept item %d still in a group", it.ID)
		}
	}
	if _, err := s.GetGroup(ctx, gid); !errors.Is(err, ErrNotFound) {
		t.Errorf("group should be gone: %v", err)
	}
	if _, err := s.DeleteGroup(ctx, gid, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice = %v", err)
	}

	// Undo, a little later: the group is back with its old id and its items moved back; the checked one stays checked.
	c.advance(30 * time.Second)
	back, err := s.RestoreGroup(ctx, gid)
	if err != nil || back.ID != gid || len(back.Items) != 3 || back.CheckedCount != 1 {
		t.Fatalf("restore: %+v %v", back, err)
	}
	if again, err := s.RestoreGroup(ctx, gid); err != nil || again.ID != gid || len(again.Items) != 3 {
		t.Errorf("second restore: %v", err)
	}
	if _, err := s.RestoreGroup(ctx, 4242); !errors.Is(err, ErrNotFound) {
		t.Errorf("restore of a group that never existed = %v", err)
	}

	// Delete with the items, restore: old ids come back.
	if _, err := s.DeleteGroup(ctx, gid, false); err != nil {
		t.Fatal(err)
	}
	left, _ := s.List(ctx, true)
	if len(left) != 0 {
		t.Fatalf("items should be deleted too: %d left", len(left))
	}
	back, err = s.RestoreGroup(ctx, gid)
	if err != nil || len(back.Items) != 3 || !slices.Equal(ids(back.Items), res.ItemIDs) {
		t.Fatalf("restore after delete: %+v %v (want ids %v)", ids(back.Items), err, res.ItemIDs)
	}
	if !back.Items[2].CheckedAt.Valid {
		t.Errorf("checked state should survive")
	}

	// Too late.
	if _, err := s.DeleteGroup(ctx, gid, false); err != nil {
		t.Fatal(err)
	}
	c.advance(RestoreWindow + time.Second)
	if _, err := s.RestoreGroup(ctx, gid); !errors.Is(err, ErrGone) {
		t.Errorf("late restore = %v, want ErrGone", err)
	}
}

func TestDeleteTemplateAlwaysDeletesItems(t *testing.T) {
	s, ctx := testService(t)
	tpl, _ := s.CreateGroup(ctx, GroupInput{Name: "T", Kind: KindTemplate, Items: lines("a", "b")})
	del, err := s.DeleteGroup(ctx, tpl.Group.ID, true)
	if err != nil || len(del.Kept) != 0 || len(del.DeletedItemIDs) != 2 {
		t.Errorf("%+v %v", del, err)
	}
	if items, _ := s.List(ctx, true); len(items) != 0 {
		t.Errorf("template items leaked into the list: %d", len(items))
	}
}

func TestTemplates(t *testing.T) {
	s, ctx := testService(t)
	milk := product(t, s, "Arla melk")
	src, _ := s.CreateGroup(ctx, GroupInput{Name: "Jerk chicken", Note: ptr("friday"), Items: []Input{{ProductID: &milk, Qty: ptr(2.0)}, {Text: "kip"}}})
	if _, err := s.SetChecked(ctx, src.ItemIDs[1], true); err != nil {
		t.Fatal(err)
	}

	tpl, err := s.SaveAsTemplate(ctx, src.Group.ID, "", "t-1")
	if err != nil || !tpl.Created || tpl.Group.Kind != KindTemplate || tpl.Group.Name != "Jerk chicken" || tpl.Group.ItemCount != 2 {
		t.Fatalf("save: %+v %v", tpl, err)
	}
	for _, it := range tpl.Group.Items {
		if it.CheckedAt.Valid {
			t.Errorf("template item %q is checked", it.Text)
		}
	}
	if again, err := s.SaveAsTemplate(ctx, src.Group.ID, "", "t-1"); err != nil || again.Created || again.Group.ID != tpl.Group.ID || !slices.Equal(again.ItemIDs, tpl.ItemIDs) {
		t.Errorf("save repeat: %+v %v", again, err)
	}
	if _, err := s.SaveAsTemplate(ctx, tpl.Group.ID, "", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("template of a template = %v", err)
	}

	// Templates are not shopping: their items are not on the list and not in the feed; the group is.
	items, _ := s.List(ctx, true)
	if len(items) != 2 {
		t.Errorf("list has %d items, want only the checklist's 2", len(items))
	}
	ch, _ := s.ChangesSinceRev(ctx, 0)
	if len(ch.Items) != 2 || len(ch.Groups) != 2 {
		t.Errorf("feed: %d items %d groups, want 2 and 2", len(ch.Items), len(ch.Groups))
	}
	tpls, _ := s.ListGroups(ctx, KindTemplate, false, false)
	if len(tpls) != 1 || tpls[0].ItemCount != 2 {
		t.Errorf("templates = %+v", tpls)
	}

	// Start two lists from it; the second under a new name. Each is a normal, unchecked checklist.
	one, err := s.Instantiate(ctx, tpl.Group.ID, "", "i-1", "")
	if err != nil || !one.Created || one.Group.Kind != KindChecklist || one.Group.Name != "Jerk chicken" || len(one.ItemIDs) != 2 {
		t.Fatalf("instantiate: %+v %v", one, err)
	}
	two, err := s.Instantiate(ctx, tpl.Group.ID, "Jerk chicken (zaterdag)", "i-2", "")
	if err != nil || two.Group.Name != "Jerk chicken (zaterdag)" || two.Group.ID == one.Group.ID {
		t.Fatalf("second: %+v %v", two, err)
	}
	if one.Group.Items[0].Qty.Float64 != 2 || !one.Group.Items[0].ProductID.Valid || one.Group.CheckedCount != 0 {
		t.Errorf("copy lost data: %+v", one.Group.Items[0])
	}
	if rep, err := s.Instantiate(ctx, tpl.Group.ID, "", "i-1", ""); err != nil || rep.Created || rep.Group.ID != one.Group.ID {
		t.Errorf("instantiate retry: %+v %v", rep, err)
	}
	gs, _ := s.ListGroups(ctx, KindChecklist, false, false)
	if len(gs) != 3 {
		t.Errorf("checklists = %d, want 3 (source + two instances)", len(gs))
	}
	if _, err := s.Instantiate(ctx, src.Group.ID, "", "", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("instantiating a checklist = %v", err)
	}
	if _, err := s.Instantiate(ctx, 999, "", "", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown template = %v", err)
	}
	// Editing the instance leaves the template alone.
	if _, err := s.AddItems(ctx, one.Group.ID, lines("extra"), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetGroup(ctx, tpl.Group.ID); got.ItemCount != 2 {
		t.Errorf("template changed: %d items", got.ItemCount)
	}
}

func TestClearCheckedLeavesGroupProgress(t *testing.T) {
	s, ctx := testService(t)
	g, _ := s.CreateGroup(ctx, GroupInput{Name: "BBQ", Items: lines("kip")})
	main, _, _ := s.Add(ctx, Input{Text: "brood"})
	_, _ = s.SetChecked(ctx, g.ItemIDs[0], true)
	_, _ = s.SetChecked(ctx, main.ID, true)
	n, err := s.ClearChecked(ctx)
	if err != nil || n != 1 {
		t.Fatalf("cleared %d, %v", n, err)
	}
	got, _ := s.GetGroup(ctx, g.Group.ID)
	if got.CheckedCount != 1 {
		t.Errorf("group progress lost: %+v", got)
	}
}

func TestRevCursorAndTombstones(t *testing.T) {
	s, ctx := testService(t)
	c := withClock(s)

	// A new phone starts at 0 and gets everything, marked as a full copy.
	g, _ := s.CreateGroup(ctx, GroupInput{Name: "BBQ", Items: lines("kip", "bier", "brood")})
	solo, _, _ := s.Add(ctx, Input{Text: "pepper"})
	ch, err := s.ChangesSinceRev(ctx, 0)
	if err != nil || !ch.FullResync || len(ch.Items) != 4 || len(ch.Groups) != 1 || ch.Rev == 0 {
		t.Fatalf("bootstrap: %+v %v", ch, err)
	}
	cursor := ch.Rev

	// Nothing happened: nothing comes back (no repeated boundary second, unlike the time cursor).
	ch, _ = s.ChangesSinceRev(ctx, cursor)
	if ch.FullResync || len(ch.Items)+len(ch.Groups)+len(ch.DeletedIDs)+len(ch.DeletedGroupIDs) != 0 || ch.Rev != cursor {
		t.Fatalf("idle: %+v", ch)
	}

	// Several writes inside one second, each seen exactly once.
	_, _ = s.SetChecked(ctx, g.ItemIDs[0], true)
	_, _ = s.Update(ctx, solo.ID, Input{Text: "pepper", Qty: ptr(3.0)})
	if err := s.Delete(ctx, g.ItemIDs[1]); err != nil {
		t.Fatal(err)
	}
	ch, _ = s.ChangesSinceRev(ctx, cursor)
	got := ids(ch.Items)
	slices.Sort(got)
	if !slices.Contains(got, g.ItemIDs[0]) || !slices.Contains(got, solo.ID) || len(got) != 2 {
		t.Errorf("items since cursor = %v", got)
	}
	if !slices.Equal(ch.DeletedIDs, []int64{g.ItemIDs[1]}) || len(ch.Groups) != 1 {
		t.Errorf("deleted=%v groups=%d (the group's updated_at moves when an item leaves)", ch.DeletedIDs, len(ch.Groups))
	}
	cursor = ch.Rev
	early := cursor // a phone that went away here has not heard about the deletions below

	// Deleting the group with its items: group tombstone plus item tombstones.
	if _, err := s.DeleteGroup(ctx, g.Group.ID, false); err != nil {
		t.Fatal(err)
	}
	ch, _ = s.ChangesSinceRev(ctx, cursor)
	if !slices.Equal(ch.DeletedGroupIDs, []int64{g.Group.ID}) || len(ch.DeletedIDs) != 2 || len(ch.Items) != 0 {
		t.Errorf("after group delete: groups=%v items=%v changed=%d", ch.DeletedGroupIDs, ch.DeletedIDs, len(ch.Items))
	}
	afterDelete := ch.Rev

	// Deleting with keep: the kept items arrive as changed items (group_id gone), the group as deleted.
	g2, _ := s.CreateGroup(ctx, GroupInput{Name: "B2", Items: lines("a", "b")})
	ch, _ = s.ChangesSinceRev(ctx, afterDelete)
	cursor = ch.Rev
	_, _ = s.DeleteGroup(ctx, g2.Group.ID, true)
	ch, _ = s.ChangesSinceRev(ctx, cursor)
	if len(ch.Items) != 2 || ch.Items[0].GroupID.Valid || !slices.Equal(ch.DeletedGroupIDs, []int64{g2.Group.ID}) || len(ch.DeletedIDs) != 0 {
		t.Errorf("keep: %+v", ch)
	}

	// Restore: the group comes back as a change and the old tombstone is cleared.
	cursor = ch.Rev
	if _, err := s.RestoreGroup(ctx, g2.Group.ID); err != nil {
		t.Fatal(err)
	}
	ch, _ = s.ChangesSinceRev(ctx, cursor)
	if len(ch.Groups) != 1 || ch.Groups[0].ID != g2.Group.ID || len(ch.DeletedGroupIDs) != 0 || len(ch.Items) != 2 {
		t.Errorf("restore feed: groups=%d deletedGroups=%v items=%d", len(ch.Groups), ch.DeletedGroupIDs, len(ch.Items))
	}
	stale := early

	// A cursor from the future is refused.
	if _, err := s.ChangesSinceRev(ctx, ch.Rev+100); !errors.Is(err, ErrInvalid) {
		t.Errorf("future cursor = %v", err)
	}
	if _, err := s.ChangesSinceRev(ctx, -1); !errors.Is(err, ErrInvalid) {
		t.Errorf("negative cursor = %v", err)
	}

	// 31 days pass; the tombstones are forgotten. The stale phone cannot be told what was deleted, so it is
	// given the whole list and told to replace its copy. A phone that synced after the pruning is fine.
	c.advance(31 * 24 * time.Hour)
	_, _, _ = s.Add(ctx, Input{Text: "late"})
	ch, err = s.ChangesSinceRev(ctx, stale)
	if err != nil || !ch.FullResync || len(ch.Items) != 4 || len(ch.Groups) != 1 || len(ch.DeletedIDs) != 0 {
		t.Errorf("stale phone: full=%v items=%d groups=%d err=%v", ch.FullResync, len(ch.Items), len(ch.Groups), err)
	}
	fresh := ch.Rev
	_, _, _ = s.Add(ctx, Input{Text: "later"})
	ch, _ = s.ChangesSinceRev(ctx, fresh)
	if ch.FullResync || len(ch.Items) != 1 {
		t.Errorf("fresh phone: %+v", ch)
	}
	// The time cursor keeps working and now also reports groups and the rev.
	old, err := s.Changes(ctx, c.now().Add(-time.Hour))
	if err != nil || old.Rev != ch.Rev || len(old.Items) != 2 || old.FullResync {
		t.Errorf("time cursor: %+v %v", old, err)
	}
	if old, _ = s.Changes(ctx, c.now().Add(-40*24*time.Hour)); !old.FullResync {
		t.Errorf("time cursor older than the window should full-resync")
	}
}

func TestTimeCursorReportsGroupDeletions(t *testing.T) {
	s, ctx := testService(t)
	c := withClock(s)
	since := c.now()
	g, _ := s.CreateGroup(ctx, GroupInput{Name: "BBQ", Items: lines("kip")})
	c.advance(time.Minute)
	_, _ = s.DeleteGroup(ctx, g.Group.ID, false)
	ch, err := s.Changes(ctx, since)
	if err != nil || !slices.Equal(ch.DeletedGroupIDs, []int64{g.Group.ID}) || !slices.Equal(ch.DeletedIDs, g.ItemIDs) {
		t.Errorf("%+v %v", ch, err)
	}
}

func TestEveryWriteMovesTheRev(t *testing.T) {
	s, ctx := testService(t)
	it, _, _ := s.Add(ctx, Input{Text: "melk"})
	r0, _ := s.Rev(ctx)
	steps := []func() error{
		func() error { _, err := s.SetChecked(ctx, it.ID, true); return err },
		func() error { _, err := s.SetChecked(ctx, it.ID, false); return err },
		func() error { _, err := s.Update(ctx, it.ID, Input{Text: "melk 2"}); return err },
		func() error { return s.Delete(ctx, it.ID) },
	}
	prev := r0
	for i, f := range steps {
		if err := f(); err != nil {
			t.Fatal(err)
		}
		r, _ := s.Rev(ctx)
		if r <= prev {
			t.Errorf("step %d did not move the rev (%d -> %d)", i, prev, r)
		}
		prev = r
	}
	// The rev stored on a row is the counter value of its last write.
	a, _, _ := s.Add(ctx, Input{Text: "a"})
	r, _ := s.Rev(ctx)
	if a.Rev != r {
		t.Errorf("item rev %d, counter %d", a.Rev, r)
	}
}

func TestReusedItemIDClearsTombstone(t *testing.T) {
	s, ctx := testService(t)
	a, _, _ := s.Add(ctx, Input{Text: "a"})
	if err := s.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	// SQLite hands the freed highest id to the next item; its tombstone must not delete the new item.
	b, _, _ := s.Add(ctx, Input{Text: "b"})
	ch, _ := s.ChangesSinceRev(ctx, 0)
	if b.ID == a.ID && len(ch.DeletedIDs) != 0 {
		t.Errorf("tombstone for a reused id survived: %v", ch.DeletedIDs)
	}
}

func TestMigrationBackfillsExistingList(t *testing.T) {
	ctx := context.Background()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := store.MigrateTo(ctx, conn, 17); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO shopping_list_items (id, text, created_at, updated_at) VALUES (1, 'melk', '2026-10-01T10:00:00Z', '2026-10-03T10:00:00Z')`,
		`INSERT INTO shopping_list_items (id, text, created_at, updated_at) VALUES (2, 'brood', '2026-10-01T11:00:00Z', '2026-10-02T10:00:00Z')`,
		`INSERT INTO shopping_list_deletions (item_id, deleted_at) VALUES (7, '2026-10-05T10:00:00Z')`,
		`INSERT INTO shopping_list_client_ids (client_id, item_id, created_at) VALUES ('c1', 1, 't')`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	s := New(conn, match.NewMatcher(conn), stats.New(conn, time.UTC)).WithLocation(time.UTC)
	// Revs follow last-touched order, the old tombstone comes after them, the counter is past all of it.
	var ckind string
	var rev1, rev2, revT, counter int64
	conn.QueryRow(`SELECT rev FROM shopping_list_items WHERE id = 1`).Scan(&rev1)
	conn.QueryRow(`SELECT rev FROM shopping_list_items WHERE id = 2`).Scan(&rev2)
	conn.QueryRow(`SELECT rev FROM shopping_list_tombstones WHERE kind = 'item' AND id = 7`).Scan(&revT)
	conn.QueryRow(`SELECT rev FROM shopping_list_rev`).Scan(&counter)
	if !(rev2 < rev1 && rev1 < revT && revT == counter && rev2 >= 1) {
		t.Errorf("revs: brood=%d melk=%d tombstone=%d counter=%d", rev2, rev1, revT, counter)
	}
	if err := conn.QueryRow(`SELECT kind FROM shopping_list_client_ids WHERE client_id = 'c1'`).Scan(&ckind); err != nil || ckind != "item" {
		t.Errorf("old client ids should be items: %q %v", ckind, err)
	}
	ch, err := s.ChangesSinceRev(ctx, 0)
	if err != nil || len(ch.Items) != 2 || ch.Rev != counter {
		t.Errorf("%+v %v", ch, err)
	}
	// The old phone's retry of an offline add still replays.
	it, created, err := s.Add(ctx, Input{Text: "melk", ClientID: "c1"})
	if err != nil || created || it.ID != 1 {
		t.Errorf("replay of an old client id: %+v %v %v", it, created, err)
	}
	// And the migration goes down and up again.
	if err := store.MigrateDownTo(ctx, conn, 17); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM shopping_list_deletions`).Scan(&n); err != nil || n != 1 {
		t.Errorf("down should restore the deletions table: %d %v", n, err)
	}
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
