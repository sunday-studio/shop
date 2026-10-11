---
title: What Shop is
group: Getting started
summary: A grocery receipt tracker you run yourself.
screen: home
---

## What it does

Photograph a paper receipt. Shop reads it on your own server, files it, and tells you what you spent, where things are cheapest and what got more expensive.

- Reads receipts with local OCR and checks that the lines add up to the printed total. Receipts that don't add up are flagged for review, not guessed.
- Creates products from your receipts. Anything uncertain waits in a review queue.
- Shows spending by month, shop and category, and price changes for the things you buy.
- Scans product barcodes and looks them up in Open Food Facts.
- Keeps a shopping list that works offline.
- Lets an AI assistant answer questions about your spending.

## How it works

1. Install the server on a machine with Docker. See [Install the server](install).
2. Connect the iPhone app to it. See [Connect the iPhone app](connect).
3. Photograph or import your receipts. See [Add a receipt](add-a-receipt).
4. Review the ones Shop flags, then read your numbers. See [Review and fix a receipt](review-a-receipt) and [Analytics](analytics).

## Before you start

> **Note:** Shop is built for Dutch receipts (Albert Heijn, Dirk and similar). The parser, the dictionary and the category rules are Dutch. It runs on an iPhone, and the server speaks plain HTTP, so keep it on your home network or a VPN. See [Limits and privacy](limits-and-privacy).

Everything runs on your own machine, in Docker, on the CPU. Apart from two optional lookups (Open Food Facts for barcodes, logo.dev for shop logos), nothing is sent to a cloud service.

Shop is open source under the MIT licence: [github.com/sunday-studio/shop](https://github.com/sunday-studio/shop).
