package translate

import (
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	cases := map[string]string{
		"Halfvolle melk":            "Semi-skimmed milk",
		"Volkorenbrood":             "Wholemeal bread",
		"Kipfilet":                  "Chicken breast",
		"Kippensoep":                "Chicken soup",
		"Jonge kaas":                "Young cheese",
		"Gouda jong 48+":            "Gouda young 48+",
		"Zure room":                 "Sour cream",
		"Scharreleieren 10 st":      "Free-range eggs 10 pieces",
		"Appels Elstar":             "Apples Elstar",
		"Bananen":                   "Bananas",
		"Worteltjes":                "Baby carrots",
		"Gerookte zalm":             "Smoked salmon",
		"Rundergehakt":              "Beef mince",
		"Wc papier 8 rollen":        "Toilet paper 8 rolls",
		"Haverdrank barista":        "Oat drink barista",
		"Crème fraîche":             "Crème fraîche",
		"Biologische volle yoghurt": "Organic whole yoghurt",
		"Tomatensoep":               "Tomato soup",
		"Aardbeienjam":              "Strawberry jam",
		"Pindakaas":                 "Peanut butter",
		"ah halfvol melk 1,5l":      "Ah semi-skimmed milk 1,5l",
		"AH HALFV MELK 1,5L":        "AH semi-skimmed milk 1,5L",
		"Kaas, jong":                "Cheese, young",
	}
	for in, want := range cases {
		got := Translate(in)
		if got.Text != want {
			t.Errorf("Translate(%q) = %q (known %d/%d), want %q", in, got.Text, got.Known, got.Words, want)
		}
		if !got.OK() {
			t.Errorf("Translate(%q) should be usable: %+v", in, got)
		}
	}
}

func TestTranslateUnknownPassesThrough(t *testing.T) {
	r := Translate("Unox Cup-a-Soup")
	if r.OK() || r.Known != 0 {
		t.Errorf("unknown words should not count as a translation: %+v", r)
	}
	if r.Text != "Unox Cup-a-Soup" {
		t.Errorf("text = %q", r.Text)
	}
}

func TestSpansAndStatus(t *testing.T) {
	SetUserTerms(nil)
	r := Translate("Calvé pindakaas met Calvé saus")
	if r.Status() != StatusPartial || r.Text != "Calvé peanut butter with Calvé sauce" {
		t.Fatalf("result: %q %s", r.Text, r.Status())
	}
	if len(r.Unknown) != 2 {
		t.Fatalf("unknown spans: %+v", r.Unknown)
	}
	// UTF-16 offsets: "Calvé" is 5 units; second occurrence after "Calvé peanut butter with ".
	want := []Span{{"Calvé", "calve", 0, 5}, {"Calvé", "calve", 25, 30}}
	for i, w := range want {
		if r.Unknown[i] != w {
			t.Errorf("span %d = %+v, want %+v", i, r.Unknown[i], w)
		}
	}
	// Input spans point into the Dutch text.
	if in := r.UnknownInput[1]; in.Start != 20 || in.End != 25 {
		t.Errorf("input span = %+v", in)
	}

	if m := Translate("Unox Knorr"); m.Status() != StatusMissing || len(m.UnknownInput) != 2 {
		t.Errorf("missing: %+v", m)
	}
	if d := Translate("7UP 330ml"); d.Status() != StatusDictionary {
		t.Errorf("no translatable words should count as complete: %+v", d)
	}
}

func TestUserTerms(t *testing.T) {
	defer SetUserTerms(nil)
	SetUserTerms(nil)
	if r := Translate("Appels Elstar"); r.Status() != StatusPartial {
		t.Fatalf("before: %+v", r)
	}
	// Preview without saving.
	if r := TranslateWith("Appels Elstar", "elstar", Term{KeepAsIs: true}); r.Status() != StatusDictionary || r.Text != "Apples Elstar" {
		t.Errorf("preview keep-as-is: %+v", r)
	}
	if r := Translate("Appels Elstar"); r.Status() != StatusPartial {
		t.Errorf("preview must not persist: %+v", r)
	}

	SetUserTerms(map[string]Term{"Calvé": {English: "Calvé brand"}, "jonagold": {KeepAsIs: true}, "melk": {English: "moo juice"}})
	if r := Translate("Calvé pindakaas"); r.Text != "Calvé brand peanut butter" || !r.Complete {
		t.Errorf("user term (accent-insensitive key): %+v", r)
	}
	if r := Translate("Appels Jonagold"); r.Text != "Apples Jonagold" || !r.Complete {
		t.Errorf("keep as is: %+v", r)
	}
	// User terms override the built-in glossary, also inside compounds.
	if r := Translate("Halfvolle melk"); r.Text != "Semi-skimmed moo juice" {
		t.Errorf("override: %q", r.Text)
	}
	if r := Translate("Karnemelk"); r.Text != "Buttermilk" {
		t.Errorf("exact built-in entry still wins over compound split: %q", r.Text)
	}
}

func TestAbbreviationsKeepInputOffsets(t *testing.T) {
	SetUserTerms(nil)
	r := Translate("AH HALFV MELK XYZQ")
	if r.Text != "AH semi-skimmed milk XYZQ" {
		t.Fatalf("text: %q", r.Text)
	}
	// "AH" and "XYZQ" unknown; input offsets refer to the raw receipt text.
	if len(r.UnknownInput) != 2 || r.UnknownInput[1].Start != 14 || r.UnknownInput[1].End != 18 {
		t.Errorf("input spans: %+v", r.UnknownInput)
	}
}

func TestRealReceiptWords(t *testing.T) {
	for dutch, want := range map[string]string{
		"KIP POTEN": "chicken legs", "TIJM": "thyme", "DILLE": "dill", "CITROENGRAS": "lemongrass",
		"BOOMGAARDSAP": "orchard juice", "KALFSPOTEN GEBRADEN": "veal legs roasted", "AVOCADO VERPAKT": "avocado packed",
		"GEMBER LOS": "ginger loose", "PETERSELIE KRUL": "parsley curly", "ZAKJE KERRIE": "bag curry", "WINTERPEEN": "winter carrot", "KIKKOMAN SOJASAUS": "kikkoman soy sauce", "ROZEMARIJN": "rosemary", "BOSUI": "spring onion",
	} {
		if got := Translate(dutch).Text; !strings.EqualFold(got, want) {
			t.Errorf("%q -> %q; want %q", dutch, got, want)
		}
	}
}

func TestRealReceiptWordsAreDictionaryStatus(t *testing.T) {
	for _, name := range []string{"KIP POTEN", "TIJM", "DILLE", "CITROENGRAS", "KIKKOMAN SOJASAUS", "ROZEMARIJN", "BOSUI", "COCA-COLA", "FANTA", "MAGGI HOT"} {
		if r := Translate(name); !r.OK() {
			t.Errorf("%q is not fully translated: %+v", name, r)
		}
	}
}
