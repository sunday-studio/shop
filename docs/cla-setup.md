# CLA setup (one-time, for the owner)

Recommendation: use the workflow `.github/workflows/cla.yml` (CLA Assistant Lite, `contributor-assistant/github-action@v2.6.1`). A PR template checkbox plus `Signed-off-by` is simpler, but a DCO sign-off only certifies origin; it does not grant the relicensing right, so it is not enough for your goal. A recorded explicit signature is.

## Where signatures live
`signatures/cla.json` on a dedicated branch `cla-signatures` in this repository. The action creates the file; it needs the branch to exist and to be unprotected.

## What you must do on GitHub
1. Create the branch: `git push origin main:cla-signatures` (or create it in the web UI). Do not protect it. If you protect `main`, that does not matter because signatures go to `cla-signatures`.
2. Check the allowlist in the workflow (`casprine` is a guess at your GitHub username; fix it).
3. Settings > Actions > General: allow actions from `contributor-assistant`, and set workflow permissions so the file's `permissions` block is honoured.
4. Optional: add the `cla` check as a required status check in branch protection for `main`.
5. If you later move signatures to another repo, add a repo secret `PERSONAL_ACCESS_TOKEN` (a PAT with repo scope) and the `remote-organization-name` / `remote-repository-name` inputs. Not needed for the current setup.
6. Have a lawyer review `CLA.md`, and confirm the Apache ICLA adaptation is allowed.

## Not verified (nothing was run on GitHub)
- That the action works with `pull_request_target` on this repo and the dedicated branch.
- That the `allowlist` username and bot names match exactly.
- Whether the custom comment inputs behave as the README describes.
- Whether `actions: write` is needed (the action README lists it); trim it if your test shows otherwise.
- The legal effect of a comment-based signature in your jurisdiction.
Note: `pull_request_target` runs with write tokens on fork PRs; the action does not check out PR code, so keep it that way and do not add steps that do.
