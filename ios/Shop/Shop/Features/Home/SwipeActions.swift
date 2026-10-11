import SwiftUI

/// Rows with swipe actions use `SwipeActionRow` (Design/Components/SwipeRow.swift):
/// pull to act, one action per side, release while armed commits. They live in a plain `List`.

extension View {
    /// Row chrome for rows that live in a plain full-width `List`.
    func shopListRow() -> some View {
        listRowInsets(EdgeInsets())
            .listRowSeparator(.hidden)
            .listRowBackground(Color.clear)
    }

    /// A `List` that looks like the surrounding scroll view.
    func shopListStyle() -> some View {
        listStyle(.plain)
            .scrollContentBackground(.hidden)
            .scrollIndicators(.hidden)
            .environment(\.defaultMinListRowHeight, 1)
    }
}
