# Design canvas vs built app: categories (and the tray pattern)

Source of truth: the design canvas ("Shop" Design artifact, boards `S4-Categories`, `S4a-AddCategory`, `S3-Shops`, `S3a-AddShop`, `H15-Actions`). Built = simulator, iPhone 17, mock server, 2026-10-11. Frames: `docs/design-frames/categories/` (`built-*` = before, `target-*` = after the Design/ fixes below).

## 1. What the canvas has and the app didn't

| | Canvas | Built (before) | Now |
|---|---|---|---|
| Tray title | 19 pt **bold**, on every tray | regular weight (`TypeStyle.trayTitle`) | **Fixed** (bold, all trays) |
| Tray title copy | "Add category" | "New category" | **Fixed** in `CategoryFormLayout` ("Add category"); rename keeps "Rename category" (not on the canvas) |
| Live preview | Centred pill: colour square (14, r5) + name, placeholder "Category name" in grey, updates as you type | none | **Added** `CategoryPreviewChip` |
| Name field | One grouped block (r20), inline label "Name" in a 70 pt column, 16 pt input, 50 pt row, placeholder "Category name" | Stacked tile (label above value), placeholder "Snacks" | **Added** `InlineFieldGroup` / `InlineField` |
| Colour | Label "Colour", 5 × 2 grid, 40 pt circles, selected = white check + ring 3 pt off the circle, **Blue pre-selected** | 10 small circles in one row (22 pt), no label, ring only, nothing selected | **Added** `CategoryColourGrid` |
| Save button | "Save category", flat light grey when disabled (`#ECECEA`, text `#B4B4AF`) | "Save", faded black pill (40 % opacity) | **Fixed**: label in `CategoryFormLayout`, flat disabled style for every primary button |
| Tray padding | 22 top, 20 sides, 14 gap | 22 / 16 / `lx` 14 | **Fixed** inside the layout (+4 sides) |
| Option rows (H15 pattern) | Tiles, r16, min 52, 6 gap, icon + 15/500 title + 12.5 hint | Move-products rows were plain hairline rows, nothing selected, "No category" hidden below the fold | **Added** `ChoiceRow` + `MoveProductsLayout`: tile rows, "No category" first and pre-selected, tick + outline on the chosen one |
| Destructive button | solid red when armed | delete-empty: solid red; delete-with-products: pale red tint (inconsistent in one flow) | **Fixed**: both solid red |
| Delete flow | H15: two-step inline ("Delete receipt" turns solid red "Delete now / Tap again to delete for good") | separate confirm tray | Not changed: the spec keeps a confirm tray for categories because the move choice needs room. Say if you want the inline two-step instead. |

## 2. Differences left in Features/ (concrete list for the swift agent)

`Features/Settings/ManageViews.swift`
1. `CategoryFormTray.body`: replace the body with `CategoryFormLayout(title: category == nil ? "Add category" : "Rename category", name: $name, colour: $color, errorText: isTaken ? "You already have a category called \(trimmed)." : nil, canSave: canSave, saving: saving, onSubmit: { Task { await save() } })`. Delete the `FieldTile`, the swatch `HStack` and the Save button. When saving a new category with `color == nil`, send `"blue"` (the grid shows Blue selected).
2. `MoveProductsTray.body`: replace with `MoveProductsLayout(categoryName:count:destinations:destination:working:onCancel:onDelete:)`. Destinations = the other categories as `.init(id: c.id, name: c.name, colour: c.dotColor)`; `destination` starts at `0` (No category, already first and selected). Remove `row(...)`, the `ScrollView`, and the red-tint button.
3. `CategoriesView` list, to match `S4-Categories` (reordering stays dropped by decision):
   - Header pill label: "Add category" (built "Add").
   - Row height 48 (built 46); colour mark is a 14 pt square with radius 5 (built 12 pt, radius 3: use `CategoryDot(size: 14)` and set its radius to 5 or use `ChoiceRow`'s `RoundedRectangle.continuous(5)`).
   - "N categories / Products" header sits in a `Palette.sunk` pill (padding 7 × 10, radius 10, 12.5 pt medium, ink2), not bare text.
   - No trailing chevron on category rows (the canvas has none); tap still opens rename.
   - Names: the canvas still shows the Dutch seed names (Zuivel, Groente & fruit…). The app is English by decision, so keep English; the canvas is stale there.
4. `ShopFormTray` (same pattern, `S3a-AddShop`): use `InlineFieldGroup` with rows Name, Chain, Branch, Address, City (placeholders "Shop name", "Albert Heijn, Lidl, ...", "Oudegracht", "Street and number", "Utrecht"), the helper line under it ("Give the shop a name to save it." / "You can pick it for any receipt."), button "Save shop". The built tray has a two-column tile grid and a Website field the canvas doesn't show; keep Website as a sixth row if the server still needs it, but as an inline row. `Delete` moves to the swipe action and the shop detail, not the tray.
5. `ConfirmTray` message block: no change needed; titles are bold now.

## 3. What the canvas does not have (my spec fills these in)

The canvas has no frames for: rename category, duplicate-name error, delete empty, delete with products, categories empty / loading / error. Those come from `docs/DESIGN-CATEGORIES-PRODUCTS.md` and now use the same components. If you want them on the canvas too, I can add boards `S4b` rename, `S4c` duplicate, `S4d` delete, `S4e` move products in the canvas's own style.

## 4. Where the canvas is out of date

- `S4-Categories` shows drag-to-reorder (grip, up/down buttons, "Drag to change the order…"): dropped by your decision (A to Z, charts by spend).
- Dutch category names, see above.
- No product counts from the server, no colour tokens: both exist now.
