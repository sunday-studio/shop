---
title: Products, shops and the shopping list
group: Using Shop
summary: Products appear from your receipts; you only decide the uncertain ones. Plus shops and the shopping list.
screen: products
---

## How products appear

1. When you confirm a receipt, each item line is matched to a product you already have, either by the same text at the same shop or by a very close name.
2. If there is no match, Shop creates a new product. It guesses a category from Dutch keywords and gives the product an English name from a built-in dictionary.
3. Anything uncertain goes to **New products** to check: a possible duplicate, a missing category or an odd name.
4. For each one you can **Accept** it, **Merge** it with the product it looks like, **Keep separate**, or **Discard** it.

A merge can be undone right after you make it.

> **Note:** To turn automatic products off, set `AUTO_PRODUCTS=off` in `deploy/.env`. Scanning a barcode still creates a product.

## Scan barcodes

Choose **Unpack groceries** in the add menu and point the camera at a barcode.

- A product you already have shows as known.
- A new one is created from Open Food Facts, with its name, brand, size, picture and nutrition when they are there.
- If Open Food Facts has nothing, Shop creates a basic product and asks you to name it. Only the barcode is sent to Open Food Facts.

## English names

Shop gives Dutch product names an English name from a built-in dictionary. Words it does not know go to **Needs English**, where you can teach it a word. You can change any product's name yourself, and a name you typed is never overwritten.

## Shopping list

The List tab works offline and syncs when you are back online.

- Add an item from a product, or type anything. Give it a quantity and a note.
- **Groups:** make a checklist (for example "Barbecue") with its own items. Save a checklist as a template to reuse it. Finished checklists move to the bottom and can be archived.
- **Auto-check:** when you confirm a receipt, items on your list that were on it are checked off. A message tells you how many, with **Undo**.
- **By store:** switch from **Planned** to **By store** to see the list split by the shop that is usually cheapest. Figures are rough ("about"), and each item says how many purchases it rests on.
- Products you buy often but not lately are suggested for the list.

## Categories

The default categories are Dairy, Produce, Meat & fish, Bakery, Pantry, Frozen, Drinks, Household, Personal care and Other. In Settings you can add, rename and delete categories.

## Shops

In Settings, open **Shops** to add, edit or delete a shop. A shop has a name, a chain, a branch and an address. Its logo comes from the shop's website through logo.dev if you set a key, and from its initials if you don't. Shop also suggests the shop from the header of a receipt.

Receipts without a shop show as **Unassigned** and still count in your spending.
