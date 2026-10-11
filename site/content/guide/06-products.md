---
title: Products, categories and shops
group: Using Shop
summary: Products appear from your receipts; you only decide the uncertain ones.
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
- If Open Food Facts has nothing, Shop creates a basic product and asks you to name it.

## English names

Shop gives Dutch product names an English name from a built-in dictionary. You can change any product's name yourself, and a name you typed is never overwritten.

## Categories

The default categories are Dairy, Produce, Meat & fish, Bakery, Pantry, Frozen, Drinks, Household, Personal care and Other. In Settings you can add, rename and delete categories.

## Shops

In Settings, open **Shops** to add, edit or delete a shop. A shop has a name, a chain, a branch and an address. Its logo comes from the shop's website through logo.dev if you set a key, and from its initials if you don't. Shop also suggests the shop from the header of a receipt.

Receipts without a shop show as **Unassigned** and still count in your spending.
