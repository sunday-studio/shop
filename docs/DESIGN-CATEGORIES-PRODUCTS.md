# Categories and products: CRUD spec

Status: design target. Replaces S.4 "Rename, reorder" in `design/ia.html`. User decision: **category ordering is dropped.** No manual ordering UI. Category lists are alphabetical; charts order by spend.

## 1. Audit: what is built vs what it should be

| Area | Built (Features/) | Problem | Target |
|---|---|---|---|
| Categories list (`CategoriesView`) | `List` in permanent edit mode with drag handles, `onMove` sends one PATCH per changed row; subtitle "Drag to change the order" | Ordering is dropped. Handles look like a bug. Different structure from Shops. | Same pattern as Shops: plain rows, alphabetical, tap = rename, swipe = Rename / Delete |
| Category delete | Only a "Delete" button inside the rename tray; server soft-deletes and **leaves products pointing at the dead category** | Silent orphaning. No count. No confirmation. | Delete asks where its products go (see 2.3) |
| Category create/rename | `CategoryFormTray`, errors only as a toast, `sort = order.count` | Duplicate name shows a generic toast; sort is meaningless now | Inline field error under the Name field; no sort |
| Category colour | Derived from name by `CategoryColor.swatch(for:)` (Dutch keywords, otherwise a hash) | Renaming a category can change its colour everywhere; new categories get a random-looking colour | Stored per category (2.4). Phase 2, not blocking |
| Category count | Client counts cached products | Wrong until the catalog is synced; O(n) per row | Server `product_count` |
| Products list | Search, chips, rows, swipe Add to list / Delete, `New product` row only in search | Edit is not on swipe. No way to add a product without searching or scanning. Chips follow server order. | Header `+`, swipe Edit, chips alphabetical |
| Product form | `ProductFormTray` create/edit | Category chips: no way to add a category on the spot; chips follow server order | Alphabetical, trailing `New…` chip |
| Product delete | Tray confirm, text matches design | Fine. No undo. | Keep. Undo is optional (asks) |
| Merge duplicates | Server has `POST /v1/products/{id}/merge`; no UI | Duplicates from receipts can't be cleaned | P.1.5 "Merge into…" (phase 2) |
| Empty / error / loading | Only "No products yet" and a catalog-sync label | No error state anywhere; empty category filter shows the generic empty state | See section 4 and `DESIGN-LOADING-STATES.md` |

## 2. Categories (S.4)

### 2.1 List
- Header exactly like Shops: back, large title "Categories", `Add` pill.
- Subtitle: "Listed A to Z. Charts rank them by spend."
- Section label row: "N categories" left, "Products" right.
- Row: colour dot 12 pt, name (rowTitle), product count (body, ink2), chevron. Height 46. Hairline separators as on Shops.
- Sort: `localizedCaseInsensitiveCompare` in the app; the server also returns A to Z so both agree.
- Tap = rename tray. Long press does nothing.
- Swipe trailing, icon only: Rename `pencil` (blue), Delete `trash` (red, no full swipe). No leading action.

### 2.2 Add / rename tray (`CategoryFormTray`)
- Title "New category" / "Rename category". One Name field, autofocus (waits for tray, see MOTION.md), Return = Save.
- Save disabled when empty or unchanged. While saving the button shows `LoadingLabel`.
- Duplicate name (409): field turns red, hint under it "You already have a category called Snacks." Tray stays open, no toast.
- Other failure: toast with Retry, tray stays.
- No Delete button in the tray (delete lives on swipe and in the row's confirm).
- After add: success haptic, tray closes, the new row scrolls into view and flashes the accent tint once.

### 2.3 Delete
- 0 products: `ConfirmTray` "Delete Snacks?" / "No products use it." / destructive "Delete category".
- N > 0 products: `MoveProductsTray`. Title "Delete Snacks?". Body "12 products use it. Pick where they go." Option rows (alphabetical, with dots) for every other category plus "No category" at the end; one pre-selected: "No category". Destructive button "Delete and move 12". 
- Failure (409 because products arrived meanwhile): the tray reloads the count, shows the new number, stays open.
- Success: `error` haptic is wrong for a user-chosen delete; use `warning`. Toast "Snacks deleted · 12 products moved to Other".
- Empty state: if the last category is deleted, show 2.5.

### 2.4 Colour (phase 2)
Category stores `color`, one of the ten tokens (`blue green red orange yellow cyan purple teal pink grey`). New category gets the first unused token, then wraps. The tray gets a swatch row of ten dots under Name. Existing categories are backfilled from today's name-based mapping so nothing changes visually. Until the backend ships it, the app keeps `CategoryColor.swatch(for:)`.

### 2.5 States
- Loading (no cache): 6 skeleton rows (dot + name bar + count bar). Cached: show cache, no skeleton.
- Empty: glyph `tag`, "No categories", "Categories group your products and your spend." + `Add category` button.
- Error (no cache): "Couldn't load categories", `Try again`. With cache: keep rows, banner "Showing saved categories".

## 3. Products

### 3.1 List (P)
- Header: large title, count, **`+` icon button and the barcode button** side by side. `+` opens `ProductFormTray` create (no prefill), autofocus Name.
- Chips: "All" then categories A to Z. A chip for a category with 0 products stays (it is a filter), but shows the empty state below.
- Row: unchanged (photo, name, size · category, best per unit).
- Swipe (icon only, per `DESIGN-SWIPE-ACTIONS.md`): leading Add to list (green, full swipe); trailing Edit (blue) and Delete (red, no full swipe). Edit opens the edit tray directly.
- Search no-results keeps the `New product "x"` row.

### 3.2 Create / edit tray
- Fields as built (name, brand, size, barcode, nutrition row, photo). Category chips alphabetical with a trailing dashed `New…` chip: opens the category tray in place (swap, not stack); on save it returns to the product form with that category selected and the draft intact (`ProductDraft` already lives outside the tray).
- Name required. Barcode errors inline (existing `ScanFeedback`). Save shows `LoadingLabel`.
- Creating with a barcode that belongs to another product: existing "Open it" row, unchanged.
- Failure: toast + stay open.

### 3.3 Delete
- From swipe or detail. Same `ConfirmTray`, copy as built. Success: `warning` haptic, row collapses, toast "Product deleted". Undo is optional (ask B6).
- Detail screen pops after delete.

### 3.4 Merge (phase 2)
Product detail, overflow: "Merge into another product". Tray: search field + product rows. Confirm tray: "Move N receipt names and M purchases into Whole milk 1 l? Barcode and nutrition are kept from the target." Server does the work (`POST /v1/products/{id}/merge`).

### 3.5 States
- Loading, first sync: 8 skeleton product rows (tile + two bars + price bar). Cached: rows, pull to refresh.
- Empty catalog: "No products yet" (as built), actions `Scan a barcode` and `Add by hand`.
- Empty category filter: "Nothing in Snacks yet", `Show all products`, and a secondary `Add a product`.
- Empty search: `New product` row (as built), text "No product matches “x”."
- Error: with cache, banner "Showing saved products · Try again". Without cache, full-page error with `Try again`.

## 4. Asks for backend

Priority order. B1 to B3 unblock the redesign; the rest are phase 2.

- **B1. Delete with reassignment.** `DELETE /v1/categories/{id}?move_to={categoryId|0}`. `0` means "no category". If the category has products and `move_to` is missing, reply 409 with `{"product_count": N}` and delete nothing. Move and delete in one transaction. Also reassign shopping-list free-text rows if they carry a category.
- **B2. `product_count` on categories.** Add to every item of `GET /v1/categories` (non-deleted products only).
- **B3. Alphabetical order, no `sort`.** `GET /v1/categories` returns `ORDER BY name COLLATE NOCASE`. `sort` is ignored on create and update (accept and drop it so the current app build keeps working) and may be removed from the response after the app build that stops sending it ships. Charts already group by spend; confirm `stats/spend?group_by=category` orders by cents desc.
- **B4. Case-insensitive unique name.** "snacks" and "Snacks" conflict. Return 409 with a stable `code` (`category_name_taken`) so the app shows the inline error without parsing text. Trim whitespace server-side. Reuse a soft-deleted row with the same name instead of failing on the UNIQUE constraint (today a deleted "Snacks" blocks a new "Snacks").
- **B5. Category `color`** (phase 2). Field `color` (enum of the ten tokens) on create/update/list; server assigns the first unused token if omitted; backfill from the current name mapping.
- **B6. Product restore** (optional). `POST /v1/products/{id}/restore` within 30 days, so Delete can show an Undo toast.
- **B7. Merge response** (phase 2). `POST /v1/products/{id}/merge` returns the target product plus counts moved (`aliases`, `lines`) for the toast. Confirm what happens to the barcode and nutrition of the source.
- **B8. Search by category while empty query** already works via `GET /v1/products?category_id=`; confirm it returns `cheapest` and `last_bought_at` too (the product list needs them).

## 5. For the swift agent (after backend B1 to B4)

- `CategoriesView`: remove `editMode`, `onMove`, the `order` state and the sort PATCH loop; sort alphabetical; add swipe actions; use `product_count`; Add pill header as on Shops.
- `CategoryFormTray`: drop `nextSort`, drop Delete button, inline duplicate error.
- New `MoveProductsTray` (Design/ can host the layout; I will add it as a component when asked).
- `ProductsView`: header `+`, swipe Edit, alphabetical chips, empty-category state.
- `ProductFormTray`: `New…` category chip.
- Loading and error pieces come from `DESIGN-LOADING-STATES.md`.
