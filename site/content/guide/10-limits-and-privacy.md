---
title: Limits and privacy
group: Help
summary: What Shop does not do, and what leaves your machine.
---

## Limits

- **Dutch receipts only.** Other countries need their own parser rules and dictionary.
- **iPhone only.** There is no Android app, and no App Store build yet.
- **No hosted version.** You run the server yourself.
- **Plain HTTP.** Keep the server on your home network or a VPN, never on the open internet.
- **OCR is not perfect.** A receipt that doesn't add up is flagged so you can fix it.

## Privacy

- Text recognition runs on your server, in a container on your machine.
- Your receipts, photos and products are stored on your server and in your backups. The app keeps a copy of your catalog on the phone so the list and products work offline.
- Two optional lookups leave your network. [Open Food Facts](https://world.openfoodfacts.org) is asked about a barcode when you scan one. [logo.dev](https://logo.dev) is asked for a shop's logo if you set a key. Leave the key empty and no logo requests are made.
- Each phone has its own key in the iPhone Keychain, and you can revoke it. The app uses no third-party libraries and no analytics.

## Licence and credits

Shop is released under the MIT licence.

- [Open Food Facts](https://world.openfoodfacts.org) data, under the ODbL
- [logo.dev](https://logo.dev) shop logos
- [PaddleOCR](https://github.com/PaddlePaddle/PaddleOCR) text recognition, under Apache-2.0
- [Phosphor Icons](https://phosphoricons.com), under MIT
