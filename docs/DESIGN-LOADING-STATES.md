# Loading, refreshing, empty and error states

One system for the whole app. Components: `Design/Components/Loading.swift` (skeletons, `StatefulContent`, `StaleBanner`, `LoadMoreFooter`) and `Design/Components/EmptyState.swift` (`EmptyState`, `ErrorState`). Catalog section "Loading states".

## 1. The rules

| Situation | Show | Never |
|---|---|---|
| First load, nothing cached, the shape of the content is known (lists, cards, detail pages, charts) | **Skeleton** of the real layout | Centered spinner |
| Cached data exists | The cached data, immediately. Refresh silently | Skeleton, "Updating…" label |
| Pull to refresh | The system control (`.refreshable`) only | Skeleton, custom spinner |
| Loading finishes in under 250 ms | Nothing extra: content just appears | A skeleton flash |
| Skeleton already visible | Stays at least 400 ms, then cross-fades to content (200 ms) | Blink |
| Action in progress (save, delete, send) | Spinner **inside the control** (`LoadingLabel`), control disabled | Full-screen blocker |
| Unknown shape, long job (reading a receipt, scanning) | Dedicated progress UI with a status line ("Reading receipt…") | Skeleton |
| Image loading | Tile placeholder in the image's size (`Palette.skeleton`) | Spinner |
| Next page of a long list | `LoadMoreFooter` (small spinner), retry row on failure | Skeleton rows at the bottom |
| Loaded, nothing to show | `EmptyState` | Blank screen |
| Failed, nothing cached | `ErrorState` with Try again | Toast only |
| Failed, saved copy on screen | `StaleBanner` at the top of the list, content stays | Error page, toast |
| Offline | Same as failed, with the offline wording | Treating it as a fault (red) |

`LoadingState` (the bare centered spinner) stays only for the unknown-shape cases above and is not used for list or detail first loads.

### Skeleton rules
- Same footprint as the real row (same height, same insets, same hairline), so content replaces it in place and nothing shifts.
- Bars only: rounded rects, no text, no icons. Colour `Palette.skeleton`; on a `sunk` card use `skeletonOnCard` (bars in `Palette.bg`).
- One calm opacity pulse for the whole skeleton (1.0 to 0.5, 1.1 s, ease in out). Not a shimmer sweep. Reduce Motion: no pulse.
- Count: enough rows to fill the first screen, 6 for rows, 3 for cards. Never an endless list.
- Hidden from VoiceOver. The container announces "Loading" once if the load passes 1 s.
- Header, title and tab bar are always real. Only the data region is skeleton.

### Timing (Motion tokens)
`skeletonDelay` 250 ms, `skeletonMinimum` 400 ms, `skeletonPulse` 1.1 s, `stateSwap` 200 ms ease out. Drive all of it with `StatefulContent(phase:)`; do not hand-roll `if isLoading`.

### Refresh and cache
1. Opening a screen: show cache at once; if the cache is older than the screen's freshness window, refresh in the background.
2. Background refresh failing is silent when the data is fresh enough (under 30 min), a `StaleBanner` otherwise.
3. `StaleBanner` clears itself on the next success. It does not stack with toasts: a failed manual pull to refresh shows the banner, not a toast.
4. Pull to refresh success needs no message.

### Copy
- Title says what happened in two or three words; the body says what still works and what to do. No "Oops", no "Error", no exclamation marks.
- Offline: "You're offline" / "Saved receipts still work. This needs a connection." Button: Try again.
- Server: "Couldn't load" / "The server didn't answer." (the app's own message when it has one) Button: Try again.
- Stale banner: "Couldn't reach the server. Showing saved products." / offline: "You're offline. Showing saved products." Action: Try again.
- Empty: a title that states the fact ("No products yet"), one sentence on how it fills up, at most one action. Filtered-empty says what was filtered ("Nothing in Snacks yet") and offers "Show all".
- Loading labels, where words are needed (long jobs only): present participle, ellipsis character, sentence case ("Reading receipt…").

### Motion and haptics
- State changes cross-fade (`stateSwap`); no slide, no scale.
- No haptic for loading or empty. A failed user-initiated action keeps its existing `error`/`warning` haptic; passive failures (background refresh) are silent.

## 2. Page by page

"Cache" means the screen has a local copy (catalog, receipts, list); "none" means it needs the server.

| Screen | Loading (first, no cache) | Cached | Empty | Error / offline | Notes |
|---|---|---|---|---|---|
| Home (H) | Hero total skeleton card, 2 attention-card skeletons are skipped, 6 `SkeletonReceiptRow` | Show list, refresh quietly | `EmptyState` "Scan receipts on the day" (as built) | No cache: `ErrorState`. Cache: `StaleBanner` "receipts" | Processing receipts keep their own "Reading receipt…" row |
| Home filter result | none (local) | n/a | "Nothing matches" + Clear filter | n/a | As built |
| Receipt detail (H.1) | Real header from the list row, lines `SkeletonLineRow` ×6 (replace the `redacted` placeholders) | n/a | n/a | `ErrorState`; offline wording when offline | Images: tile placeholder, "Image unavailable" with Try again |
| Receipt image | Dark tile placeholder | n/a | n/a | "Image unavailable" + Try again | Spinner allowed here: unknown size, full-screen dark |
| Analytics (A) | `SkeletonChart` + 5 `SkeletonSimpleRow` | Show cached range data | "No spending in this range" + Change range | No cache: `ErrorState`. Cache: `StaleBanner` "spending" | Range change keeps old bars at 50 % opacity until new data lands, no skeleton |
| Analytics detail (A.2, A.3) | `SkeletonCard` 120 + 5 `SkeletonProductRow` | n/a | "No big changes" (as built), "No purchases in this range" | `ErrorState` | |
| Products (P) | 8 `SkeletonProductRow` under real header, search and chips | Show catalog | "No products yet" + Scan a barcode / Add by hand; filtered: "Nothing in X yet" + Show all | No cache: `ErrorState`. Cache: `StaleBanner` "products" | Search: local results at once, "Searching…" label in the section header, never a skeleton |
| Product detail (P.1) | Hero from list row; sections `SkeletonCard` 96 ×2 + 3 `SkeletonLineRow` | Show product, refresh prices | "No purchases yet" (as built) in the price section | `ErrorState` | |
| Price history (P.1.1) | `SkeletonChart` + rows | n/a | "No purchases yet" | `ErrorState` | |
| Receipt names (P.1.4) | 3 `SkeletonLineRow` | n/a | "No receipt names yet" | `ErrorState` | |
| Product review queue | 3 `ReviewCardSkeleton` (as built) | Show cards | "All caught up" (as built) | `ErrorState`; page 2 failure: `LoadMoreFooter(.failed)` | |
| Shopping list (L) | 6 `SkeletonListRow` | Show list (offline-first, no banner when offline) | "Nothing on the list" (as built) | Sync failure: small `StaleBanner` only if the last sync is over 1 h old | Optimistic checks; no spinner on check |
| Search (X.1) | "Searching…" under the field; 4 `SkeletonProductRow` after 250 ms | n/a | Idle: "Search everything"; no hits: "Nothing found" | `ErrorState` inline under the field | |
| Settings (S) | Server card shows "Checking…" in its status pill, rows are static | n/a | n/a | Pill turns "Offline" (amber); no error page | |
| Server and sync (S.1) | Check rows: dot + `SkeletonBar` | Last known checks, greyed with time | n/a | "Couldn't check the server" row + Try again | |
| Upload queue (S.2) | none (local) | n/a | "All uploaded" (as built) | Failed uploads are rows with their own error and Retry | |
| Shops (S.3) | 5 `SkeletonSimpleRow` | Show shops | "No shops yet" + Add shop | No cache: `ErrorState`. Cache: `StaleBanner` "shops" | |
| Categories (S.4) | 6 `SkeletonSimpleRow` | Show categories | "No categories" + Add category | As Shops | |
| Add / import flow | Per `DESIGN-UPLOAD-FLOW.md` | n/a | n/a | Per upload spec | Long jobs use progress UI, not skeletons |
| Unpack (C.3) | Camera starting: black with the scan frame, no spinner | n/a | n/a | Camera denied: "Camera is off" + Open Settings | |
| Onboarding | Server test: spinner inside the Connect button | n/a | n/a | Inline field error | |
| Trays with data (pickers, merge) | 4 rows of the matching skeleton | n/a | Row-level empty text | Inline "Couldn't load" + Try again | Never resize the tray when content arrives: reserve the skeleton height |

## 3. Implementation notes for the swift agent

- Replace every `if isLoading { LoadingState() } else if let error { ErrorState… } else if empty { EmptyState… } else { content }` with `StatefulContent(phase:retry:)` and compute the phase in the model:
  `.loading` only when there is nothing to show and a request is running; `.content` when anything is cached; `.empty` when loaded and the list is empty; `.failed` only when nothing is cached.
- Inside `List`, put `StatefulContent` in a single row with `shopListRow()`; the skeleton rows already include their hairlines.
- Never add `.redacted(reason: .placeholder)`: it follows real text and shifts. `ReceiptView.linesSkeleton` and the `redacted` hero move to the skeleton components.
- Stale case: render the content and put `StaleBanner` above it in the list, not around `StatefulContent`.
- `ErrorState` now takes `offline:`; pass `NetworkMonitor` state.
- Remaining bare `ProgressView` calls are fine only inside controls and the paging footer (Settings row 426, ProductForm 308, ImportViews 222, ProductReview paging → `LoadMoreFooter`).
