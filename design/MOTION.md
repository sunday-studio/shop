# Shop motion and haptics

Live demos: `design/motion.html`. Code: `ios/Shop/Design/{Motion,Haptics,Typography}.swift`. Numbers come from `design/DESIGN-RULES.md`; this page says how to use them.

## Principles (from Family)

- **Simplicity.** One thing moves per intent. If a transition does not explain where something came from or went, remove it.
- **Fluidity.** Motion is continuous and interruptible. A tray that is dragged, tapped away or replaced mid-flight must retarget from its current position, never snap or restart. Use springs, not fixed-duration tweens, for anything the user can interrupt.
- **Delight.** Spend it in small, rewarding moments: a check-off, a total rolling, a synced pill, a shake on a wrong password. Never on navigation chrome or repeated actions.

## Tokens

| Token | Value | Use |
|---|---|---|
| spring | `cubic-bezier(0.32, 1.28, 0.5, 1)` 420ms / `spring(response: 0.42, dampingFraction: 0.78)` | trays, toggles |
| ease | `cubic-bezier(0.22, 1, 0.36, 1)` 240ms | fades, slides, tray exit |
| press | 90ms ease-out, scale 0.97 (pills and + 0.94) | every tappable |
| tray height | 360ms spring, content cross-fade 160ms | tray to tray |
| number roll | 180ms | totals, tallies |
| list stagger | 8px, 24ms step, max 8 rows, first paint only | lists |
| check-off | circle 200ms spring, strike after 160ms, collapse 240ms | list rows |
| tab slide | 12px, 200ms ease, icon pop 120ms | tab bar |

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
- Keep generators prepared (`Haptics.prepare()` on screen appear) and respect a Settings toggle.
- Haptics reinforce, never replace, a visual state. Every haptic event has a visible change.
