# Changelog

## [0.2.0](https://github.com/mikesmitty/interlock/compare/v0.1.0...v0.2.0) (2026-10-18)


### Features

* **api:** serve decoder definitions, identify decoders and read or write CVs by name ([fe05057](https://github.com/mikesmitty/interlock/commit/fe0505748745ee143a6b6a3e072f19a164c63bf7))
* **decoder:** lay out ESU decoders' own function mapping ([0eb896c](https://github.com/mikesmitty/interlock/commit/0eb896c316d016a6ab3c8bf43a45d1442c332eec))
* **decoder:** load a family as a whole and read a decoder's address ([c1d3e1a](https://github.com/mikesmitty/interlock/commit/c1d3e1af3498f68af28dcd48da2d3de09cb94c81))
* **decoder:** read JMRI's decoder definitions from a folder, GitHub or the archive ([fa25659](https://github.com/mikesmitty/interlock/commit/fa256595ff6725105041c9f95bae497b8ece8241))
* **decoder:** resolve decoder definitions, identify decoders and address their CVs ([63d9be0](https://github.com/mikesmitty/interlock/commit/63d9be0acc7125b00db40f75ba308942d75c83ab))
* **release:** ship a Windows build and sign macOS and Windows builds ([2fe8df8](https://github.com/mikesmitty/interlock/commit/2fe8df804dee37c1a519e9851b5d6717435b5d1b))
* **roster:** check the decoder on the programming track before work on it ([79f813f](https://github.com/mikesmitty/interlock/commit/79f813fec6105a69aebeb2facbce0bd49b5f57f0))
* **roster:** keep changes not written yet with a decoder's CV values ([1d5e252](https://github.com/mikesmitty/interlock/commit/1d5e252f20ed796cc2bd6d242ba73e5eda0326b2))
* **roster:** keep each locomotive's decoders with their CV values and import a JMRI roster ([95c9fee](https://github.com/mikesmitty/interlock/commit/95c9fee866b13bdfac54b984e6809aaebaf30f14))
* **roster:** keep imported CV values apart until the decoder is read ([3d8c1dd](https://github.com/mikesmitty/interlock/commit/3d8c1dd62065070c236e921e43e7df5c66229e94))
* **roster:** keep named setups of a decoder's values to load again ([69e72a7](https://github.com/mikesmitty/interlock/commit/69e72a770912ea2f817d4befb57d41204b573355))
* **roster:** track a session of work on each decoder ([641939c](https://github.com/mikesmitty/interlock/commit/641939cf490c5126a9f09daeade0a3e4c2c80e99))
* run on Windows ([771befb](https://github.com/mikesmitty/interlock/commit/771befb1fb1b31112f4f78bdbf24d1a06dd5853f))
* **sim:** simulate an ESU decoder with paged CVs on the programming track ([a537348](https://github.com/mikesmitty/interlock/commit/a53734888ab6244b9987c69e4958789a42c574ac))
* **train:** make up LCC consists on the command station ([7872ce6](https://github.com/mikesmitty/interlock/commit/7872ce600bb11c8976f03af7ee96cf811a83eea3))
* **train:** show track power as the command stations report it ([8efbcc2](https://github.com/mikesmitty/interlock/commit/8efbcc218e2eba2455396ac9af4ac7c61905ea82))
* **web:** a Roster page with each locomotive's decoders and the JMRI import ([d9dafd0](https://github.com/mikesmitty/interlock/commit/d9dafd054cc30f8d3538c4a8d49c50591394e6d3))
* **web:** ask before Write page writes values not read from the decoder ([270d91f](https://github.com/mikesmitty/interlock/commit/270d91fc4fc1f99a2cc07c0bf7682d52c8fc5ac7))
* **web:** change a decoder's settings by name ([0ee87ef](https://github.com/mikesmitty/interlock/commit/0ee87efd422ba375dad8dfdde679a6b115cadf49))
* **web:** name train nodes for their locomotives ([382a6bf](https://github.com/mikesmitty/interlock/commit/382a6bf74648b845b8e0b2d28eae2f3319366e04))
* **web:** read imported values on Write page instead of writing them ([7f572eb](https://github.com/mikesmitty/interlock/commit/7f572ebbed420504cc41f90b2370187497d49966))
* **web:** say how many values a decoder profile load takes as they are ([c12e4fa](https://github.com/mikesmitty/interlock/commit/c12e4fa8f59d2d3a957f5c595f487af9e68734f9))
* **web:** show ESU function maps and keep changes not written yet ([5559650](https://github.com/mikesmitty/interlock/commit/5559650b1b012767608ec9a3826a59885dcab67d))
* **web:** show imported CV values as not read yet ([4f47052](https://github.com/mikesmitty/interlock/commit/4f47052fcd78242f30c4bc9af8b7ea7045283d9b))
* **web:** show the roster entry on a train node's page ([f8b4a5a](https://github.com/mikesmitty/interlock/commit/f8b4a5a3a2823c24360c1c883b8457053aa47d5a))
* **web:** show where each decoder setting's value came from ([e0d2593](https://github.com/mikesmitty/interlock/commit/e0d25939c9842a6d503fb347357573c088c68009))
* **web:** trust only values read or written in this session for writes ([302d5eb](https://github.com/mikesmitty/interlock/commit/302d5ebd740e837dd30352a6ebfcb4073a7acce4))
* **withrottle:** serve throttle apps over WiThrottle ([8cae230](https://github.com/mikesmitty/interlock/commit/8cae2304366a063cecc33fae0b1dfe20a7731787))


### Bug Fixes

* **decoder:** say plainly when the definitions folder does not exist ([6714d4d](https://github.com/mikesmitty/interlock/commit/6714d4d71aec1e011135932a89f782982bd25e41))
* **release:** stop release binaries reporting a modified tree ([8c7ddbb](https://github.com/mikesmitty/interlock/commit/8c7ddbba931cc6bb28df13af89bd42dc54e96cfd))
* **web:** widen the navigation rail so Programming and Automations fit ([2fbd8db](https://github.com/mikesmitty/interlock/commit/2fbd8dbff2666a3d666584f1138dccb4ec3d53af))


### Documentation

* describe decoder settings and their sources ([dcdabaf](https://github.com/mikesmitty/interlock/commit/dcdabaf63aaf3734f6353795b3859b4af01a572b))
* describe imported CV values waiting for a read ([a9f0b24](https://github.com/mikesmitty/interlock/commit/a9f0b2494c036b7193f98d56123b123b5094a6e0))
* describe the ESU function map and changes kept until written ([415a269](https://github.com/mikesmitty/interlock/commit/415a26916033a6999c941187ee7bc48178c0f759))
* describe the roster, its decoders and the JMRI import ([c7fd2c0](https://github.com/mikesmitty/interlock/commit/c7fd2c0e70dba81d33642f62d7f7b7b0cc5c81dd))
* **design:** describe the goals without comparing Interlock to JMRI ([704d8ba](https://github.com/mikesmitty/interlock/commit/704d8ba1bed049c66361ebe04bcb6790ddbead6d))
* **design:** list consist support under Later ([04aeff9](https://github.com/mikesmitty/interlock/commit/04aeff9a7ef8b3bef122c8cd40cf9dc6aa9a0495))
* **design:** plan decoder programming as Phase 7 ([a0edaf2](https://github.com/mikesmitty/interlock/commit/a0edaf2ffa51353a6f196226385039e5d0a5585d))
* explain the setting lamps and decoder sessions ([9fcd0c0](https://github.com/mikesmitty/interlock/commit/9fcd0c05503886ab6d5904110c58b842e56230d5))
* **readme:** describe Interlock without comparing it to JMRI ([9d4be24](https://github.com/mikesmitty/interlock/commit/9d4be24aa98773cc1f1a75b47df65fce1affeb46))


### Performance

* **decoder:** fetch a definition's files several at a time ([61a5d6b](https://github.com/mikesmitty/interlock/commit/61a5d6bb4870aacd281f6ea5f3305706c11bfddc))


### Refactoring

* **roster:** rename decoder setups to decoder profiles ([0d79545](https://github.com/mikesmitty/interlock/commit/0d79545fa452f89a24d9a933ca75bba2ce63abf4))


### Tests

* **train:** change the simulated decoder under the station's lock ([f77bf13](https://github.com/mikesmitty/interlock/commit/f77bf13da2e656b80f505a7ecb6c6c6e1edad5c0))

## 0.1.0 (2026-10-17)


### Features

* **automation:** automations with triggers, conditions, actions and traces ([9be0848](https://github.com/mikesmitty/interlock/commit/9be0848fa56dfb8f85db57cf55314b563bf335b2))
* **bus:** LCC bus layer, local node, discovery, monitor and web pages ([7b85e21](https://github.com/mikesmitty/interlock/commit/7b85e2130b0fc2776438e780c93faa82878af8e4))
* **config:** memory configuration client, CDI parser and node configuration forms ([f47eb83](https://github.com/mikesmitty/interlock/commit/f47eb8356bb40635851d932412336d191cfb21fc))
* **entities:** named event table, node profiles and entities with live state ([bbdfc99](https://github.com/mikesmitty/interlock/commit/bbdfc9966536ef060f413ca46a277042b574deb4))
* **firmware:** firmware upgrades, the fast clock, backup and restore, OpenLCB TCP and SocketCAN links, docs site ([bcc962e](https://github.com/mikesmitty/interlock/commit/bcc962e1fe51f7727774a2cf6cff428eabf79387))
* project skeleton with server, store and web scaffold ([24ebee2](https://github.com/mikesmitty/interlock/commit/24ebee2e61e172bd9dbbeeb2ec2d9a8ac60a8c27))
* **trains:** throttle, roster, CV programming and the train automation action ([c751ece](https://github.com/mikesmitty/interlock/commit/c751ece345f3f05a2b58e8441e1f79fac6b2cd94))
* Update dependency @vitejs/plugin-react to v6 ([#15](https://github.com/mikesmitty/interlock/issues/15)) ([6127635](https://github.com/mikesmitty/interlock/commit/61276356ea8daa4b4b82347727d5e3cdb245a23f))
* Update dependency react-router to v8 ([#16](https://github.com/mikesmitty/interlock/issues/16)) ([faef826](https://github.com/mikesmitty/interlock/commit/faef826082374ec1a6cb4b13a9f2d8dcc614f360))
* Update dependency typescript to v7 ([#17](https://github.com/mikesmitty/interlock/issues/17)) ([6b61ba5](https://github.com/mikesmitty/interlock/commit/6b61ba585945c820b4dc5a3264db3086cd85679a))
* Update dependency vite to v8 ([#18](https://github.com/mikesmitty/interlock/issues/18)) ([8304b99](https://github.com/mikesmitty/interlock/commit/8304b995aec4bc294ae658b995c5dc8219c0173d))
* Update module golang.org/x/sys to v0.49.0 ([#2](https://github.com/mikesmitty/interlock/issues/2)) ([52d86a4](https://github.com/mikesmitty/interlock/commit/52d86a43714078355a49053ba6cb7909cbf0dbea))


### Bug Fixes

* **web:** keep node names on one line in narrow tables ([7eddd5e](https://github.com/mikesmitty/interlock/commit/7eddd5e29e18fdf356e46a8fe0b078b04ec4907d))


### Miscellaneous

* Configure Renovate ([#1](https://github.com/mikesmitty/interlock/issues/1)) ([b64ef63](https://github.com/mikesmitty/interlock/commit/b64ef635659cb51a5d7454ca58a9025392c6de9a))
* **release:** make the first release 0.1.0 ([6ec745b](https://github.com/mikesmitty/interlock/commit/6ec745b607f6beac453544dedcb29a1cd166c9f7))
* Update actions/checkout action to v7 ([#3](https://github.com/mikesmitty/interlock/issues/3)) ([44d945a](https://github.com/mikesmitty/interlock/commit/44d945a60b1d07303f8dec631138ad2632eb61e2))
* Update actions/configure-pages action to v6 ([#9](https://github.com/mikesmitty/interlock/issues/9)) ([a157f8a](https://github.com/mikesmitty/interlock/commit/a157f8af8bbf0a1132f8eaf788f2a75e42abaa08))
* Update actions/deploy-pages action to v5 ([#10](https://github.com/mikesmitty/interlock/issues/10)) ([f34bdb7](https://github.com/mikesmitty/interlock/commit/f34bdb7ae3389c52c907a875e67ef257a5f94aa2))
* Update actions/setup-go action to v7 ([#6](https://github.com/mikesmitty/interlock/issues/6)) ([8b241ef](https://github.com/mikesmitty/interlock/commit/8b241ef7a2c8869722141c292ebe11817c089c61))
* Update actions/setup-node action to v7 ([#11](https://github.com/mikesmitty/interlock/issues/11)) ([19562de](https://github.com/mikesmitty/interlock/commit/19562de58c0386f7bb801f1cc1f627048beed9a7))
* Update actions/upload-artifact action to v7 ([#12](https://github.com/mikesmitty/interlock/issues/12)) ([c7fb53a](https://github.com/mikesmitty/interlock/commit/c7fb53ac54cda40d0e54bfc3f6728e9dbe1cef9a))
* Update actions/upload-pages-artifact action to v5 ([#13](https://github.com/mikesmitty/interlock/issues/13)) ([d868033](https://github.com/mikesmitty/interlock/commit/d86803331bf3363417e9d5687e33c13be95051a5))
* Update dependency node to v24 ([#14](https://github.com/mikesmitty/interlock/issues/14)) ([9a98e28](https://github.com/mikesmitty/interlock/commit/9a98e288cf9a9503a751729073b03792aa9b9a0d))
* Update golangci/golangci-lint-action action to v9 ([#7](https://github.com/mikesmitty/interlock/issues/7)) ([f47461b](https://github.com/mikesmitty/interlock/commit/f47461b9f9195cd4278654d266d78c6f16e92a47))
* Update googleapis/release-please-action action to v5 ([#8](https://github.com/mikesmitty/interlock/issues/8)) ([ad80b5f](https://github.com/mikesmitty/interlock/commit/ad80b5f36a95c0497cee50d4be4f24be23adf023))


### Documentation

* require Node 22.22 or newer to build ([045a2d7](https://github.com/mikesmitty/interlock/commit/045a2d74cc1a9e9fdfb4bab2a7c0658b4165ca3f))


### Tests

* **clock:** wait for the midnight tick, not the computed date ([7ac9ac2](https://github.com/mikesmitty/interlock/commit/7ac9ac24f987c9e12f4c879d0db76b2518ec0f21))
