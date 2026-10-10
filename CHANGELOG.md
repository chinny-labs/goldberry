# Changelog

## [1.3.0](https://github.com/chinny/goldberry/compare/v1.2.0...v1.3.0) (2026-10-10)


### Features

* **api:** backdate entries with effective_at ([6fbd51e](https://github.com/chinny/goldberry/commit/6fbd51ecd437e063d36e2c2efd5d0b08234c7b69))
* **api:** backdate entries with effective_at ([5ac3624](https://github.com/chinny/goldberry/commit/5ac3624d514a4d9a35a566a7de305630e85945da))

## [1.2.0](https://github.com/chinny/goldberry/compare/v1.1.0...v1.2.0) (2026-10-10)


### Features

* API tokens and design for the JSON API + MCP server (1/4) ([b116bfa](https://github.com/chinny/goldberry/commit/b116bfa4fdcb22877822f2feadff3549020ff803))
* API tokens owned by the minting admin ([954f95c](https://github.com/chinny/goldberry/commit/954f95cd804e2e2c5a8a2e666c4a53d7336217a1))
* **api:** bearer auth and read endpoints under /api/v1 ([b815b01](https://github.com/chinny/goldberry/commit/b815b013e4399bd3590da9584a61d423d96b4c56))
* **api:** bearer auth and read endpoints under /api/v1 (2/4) ([d89a331](https://github.com/chinny/goldberry/commit/d89a3312e94ead93322e6b6597189210882bfd27))
* **api:** write and money endpoints with atomic batches ([b081afa](https://github.com/chinny/goldberry/commit/b081afabdabf947144c5540c5f512edcd9361ad0))
* **api:** write and money endpoints with atomic batches (3/4) ([df620ab](https://github.com/chinny/goldberry/commit/df620abdef6f41d8f23a13ed48815cf91d4f77a3))
* **mcp:** opt-in MCP server at /mcp (4/4) ([f1354d2](https://github.com/chinny/goldberry/commit/f1354d22c1e50778600425bc6c672835b37ee525))
* **mcp:** opt-in MCP server at /mcp on the same tokens ([ee2952d](https://github.com/chinny/goldberry/commit/ee2952d137dd142e03075e85035e3312c217d1a8))


### Bug Fixes

* close response bodies in the API web test; document the per-address block ([0c1745d](https://github.com/chinny/goldberry/commit/0c1745d6c9ea993757668a73151d367e023c1502))
* lint, key-reuse 422, audit via-token on the kid's page, TxLocked stress test ([91d0189](https://github.com/chinny/goldberry/commit/91d01895787edf2f6cd261cc35a9a6d71a1793ce))
* **mcp:** a lone entry's error doesn't say 'item 1'; audit docs match the page ([7ed953e](https://github.com/chinny/goldberry/commit/7ed953e3b862d539b59f87a72bf3a0e4d7ea5619))
* record the calling token as via_token so web-made token rows don't read 'via' ([7425f08](https://github.com/chinny/goldberry/commit/7425f081910e35f4ddb46f56d99967addb020a7e))
* silence gosec G101 on the api_tokens column list ([d01563f](https://github.com/chinny/goldberry/commit/d01563f7db0bc6591ce95c93a20acc7b91c06691))

## [1.1.0](https://github.com/chinny/goldberry/compare/v1.0.0...v1.1.0) (2026-10-10)


### Features

* balance graph with preset and custom ranges ([#8](https://github.com/chinny/goldberry/issues/8)) ([52f77e7](https://github.com/chinny/goldberry/commit/52f77e7a9bf3dd2920d0246ff3e368d01e57d67f))
* investing with an Invest jar at real market prices ([#9](https://github.com/chinny/goldberry/issues/9)) ([650fe62](https://github.com/chinny/goldberry/commit/650fe62440bd1fdcee71344270cb98350335775b))

## 1.0.0 (2026-10-08)


### Features

* backups, portable export/import, PWA manifest (phase 6) ([31a1b44](https://github.com/chinny/goldberry/commit/31a1b44b2fd861bf7185ac6f3d3dbc64e05bb81f))
* goals, interest, backups, Helm chart and the v1.0 release pipeline ([#3](https://github.com/chinny/goldberry/issues/3)) ([99fa914](https://github.com/chinny/goldberry/commit/99fa914c8012d73bd478ba943623917f59058070))
* jars, transfers, jar locks with the gauntlet, recurring allowance (phase 4) ([7107446](https://github.com/chinny/goldberry/commit/7107446b805ce14fd1206065dbb6b7c068751761))
* savings goals and parent-paid interest (phase 5) ([6f1b38c](https://github.com/chinny/goldberry/commit/6f1b38cc0ed543c9dd105a84a9cd95935061078d))
* store, service and auth core with contract suite ([a611013](https://github.com/chinny/goldberry/commit/a61101361525cd2628778afc2db6fe8f609b32e2))
* web app, CLI and scheduler (phases 1-2) ([5b40794](https://github.com/chinny/goldberry/commit/5b407946c6191586662904e02d61c94732dd361e))
