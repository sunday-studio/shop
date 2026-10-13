# Contributing to Shop

Thanks for helping. Shop is a self-hosted grocery product (API, OCR service, iPhone app). Contributions to the **self-hosted product** are welcome.

## What to send

- **Bug reports and small fixes**: welcome. Open an issue or go straight to a pull request.
- **Larger features or changes**: please open an issue first and wait for a reply, so you do not spend time on something that will not be merged.
- Out of scope: the owner's paid hosted service and anything unrelated to the self-hosted product.

## Contributor Licence Agreement (required)

Before a pull request can be merged you must agree to the [CLA](CLA.md). In short: you keep your copyright, and you give the owner a permanent licence to use and relicense your contribution, including in a paid hosted service. Read the plain-words summary at the top of the CLA.

How to sign: when you open a pull request, a bot comments with instructions. Reply with the exact sentence:

> I have read the CLA Document and I hereby sign the CLA

You sign once; it covers your later pull requests. The owner and bots are exempt.

## Code style and tests

- Go (in `api/`): keep `gofmt` clean and `go vet` happy. Run `make vet` and `make lint`.
- Run `make test` (Go unit tests) before every pull request.
- Run `make e2e` for changes to the API, jobs or the OCR path. It runs in an isolated Docker stack and does not touch your live stack.
- Run `make restore-drill` only if you change backup or restore code.
- `make help` lists every target.
- Keep changes focused, and add or update a test with a fix.

## Licence reminder

Shop is under the [PolyForm Noncommercial License 1.0.0](LICENSE): personal and non-commercial use. By contributing you license your work to the owner as set out in the [CLA](CLA.md); you do not sign away your copyright.

## Not covered

The GT America font files in this repository are a separate trial licence from Grilli Type. They are not covered by the project licence or the CLA, and must not be reused or contributed.

## For the maintainer

Setup for the signing bot is in [docs/cla-setup.md](docs/cla-setup.md).
