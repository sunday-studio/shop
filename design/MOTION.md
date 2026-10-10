# Shop motion and haptics

Live demos: `design/motion.html`. Code: `ios/Shop/Design/{Motion,Haptics,Typography}.swift`. Numbers come from `design/DESIGN-RULES.md`; this page says how to use them.

## Principles (from Family)

- **Simplicity.** One thing moves per intent. If a transition does not explain where something came from or went, remove it.
- **Fluidity.** Motion is continuous and interruptible. A tray that is dragged, tapped away or replaced mid-flight must retarget from its current position, never snap or restart. Use springs, not fixed-duration tweens, for anything the user can interrupt.
- **Delight.** Spend it in small, rewarding moments: a check-off, a total rolling, a synced pill, a shake on a wrong password. Never on navigation chrome or repeated actions.

## Tokens

| Token | Value | Use |
|---|---|---|
| spring | `cubic-bezier(0.32, 1.28, 0.5, 1)` 420ms / `spring(response: 0.42, dampingFraction: 0.78)` | toggles, pops (not trays) |
| ease | `cubic-bezier(0.22, 1, 0.36, 1)` 240ms | fades, slides, tray exit |
| press | 90ms ease-out, scale 0.97 (pills and + 0.94) | every tappable |
| sheet | `cubic-bezier(0.32, 0.72, 0, 1)` 450ms in, 350ms out (the iOS sheet / Vaul curve, no overshoot) | tray enter and exit: one translate from the bottom edge, no opacity, no scale |
| tray swap | same curve 360ms for the height; old content out 80ms, new content in 160ms after a 60ms delay | tray to tray |
| number roll | 180ms | totals, tallies |
| list stagger | 8px, 24ms step, max 8 rows, first paint only | lists |
| check-off | circle 200ms spring, strike after 160ms, collapse 240ms | list rows |
| tab slide | 12px, 200ms ease, icon pop 120ms | tab bar |

## How a tray moves (built like Vaul)

A tray is **one surface** (background, ring, content) moved with **one transform on one curve**. Nothing inside has its own entrance animation, so text and fields can never drift apart. Rules: enter and exit are a single translate from the bottom edge on the sheet curve, with no opacity and no scale (an opacity fade makes the card see-through for a moment). A tray replacing another overlays old and new content (never stacks them in layout), animates the surface height on the same curve, and crossfades the content (old 80 ms out, new 160 ms in after 60 ms; blur is allowed to mask a layout change). Dragging updates only the transform in a tiny view of its own, never the tray content; down follows the finger 1:1, up is damped (the further you pull the less it moves); release closes on velocity above 400 pt/s or a quarter of the height, otherwise it settles back on the same curve. Do not autofocus a text field before the enter animation has finished (the keyboard would move the tray while it is still moving).

## Tray or full screen

Use a **tray** for a short, self-contained decision that returns to where the user was: confirm, pick, edit one thing, filter, destructive confirmation. Consecutive trays must differ in height so the morph reads. Use a **full screen** (push) when the task has its own navigation, long scrolling content, a text field flow with a keyboard plus more than one field group, or when the user would want to go back through history. A tray never hosts another navigation stack and never opens a second tray on top; it replaces itself.

## What never animates

- Scrolling content, scroll indicators, pull-to-refresh beyond system behavior.
- Text the user is typing, and the keyboard's own motion.
- Data refreshes, sort order changes after the first paint, and badges that update in the background.
- Anything on a screen already showing for more than a moment because a sync finished, except the sync pill.
- Layout of rows beyond first paint (no re-stagger on return or on filter).
- Destructive confirmation: no bounce, no celebration.

## Reduced motion

When Reduce Motion is on: drop scale, slide, springs, stagger and the shake; keep 120ms opacity fades; number roll becomes a cross-fade; the shake is replaced by the red tint and the error haptic. Never remove feedback, only displacement. Web demos read `prefers-reduced-motion`; SwiftUI reads `accessibilityReduceMotion` (handled inside `Motion.swift` helpers).

## Haptics

Taxonomy and mapping live in `DESIGN-RULES.md` and the table in `motion.html`.

- **Less is more.** Max one haptic per user action. If an action triggers a sequence (check-off, collapse, total update), only the first beat gets a haptic.
- **Never on scroll**, on scrolling momentum, on passive animations, or on content that arrives without a touch. The exception is the outcome of a user-started operation (success when a receipt finishes).
- Haptic fires on touch-down for press-style controls and on the state change for toggles, so it lands with the visual.
- Match weight to consequence: selection for choosing, light for opening, medium for commits, soft for check-offs, notifications only for outcomes.
- **Swipe actions have two moments.** *Armed* (the drag crosses the commit line) plays `selection`, once per direction change. *Committed* plays the action's own haptic, exactly what the equivalent button or tap plays: Confirm receipt `medium`, Add to list `light`, Check off `soft`, Retry `light`, an action that opens a tray none (the tray plays `trayOpen`; `error` only after a confirmed delete). Tapping the revealed button plays only the committed haptic. Nothing removes data instantly without an Undo toast (`soft`) or a confirmation tray.
- **Barcode lookups outside the scanner** (product form): data filled from Open Food Facts `scanNew`, nothing found or OFF unreachable `selection` with an inline caption, a code that belongs to another product `warning`.
- Keep generators prepared (`Haptics.prepare()` on screen appear) and respect a Settings toggle.
- Haptics reinforce, never replace, a visual state. Every haptic event has a visible change.
