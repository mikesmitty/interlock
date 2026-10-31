# Changelog

## [0.3.0](https://github.com/mikesmitty/interlock/compare/v0.2.0...v0.3.0) (2026-10-31)


### Features

* **decoders:** count settings saved before the session as not checked ([a173f30](https://github.com/mikesmitty/interlock/commit/a173f30c88a5df31080d48fbbf65bf1ac4904ef9))
* **entities:** add canbus-outpost's signal heads ([1078e36](https://github.com/mikesmitty/interlock/commit/1078e36b43e437a02ca04b61a6665e5afca7e9ab))
* **entities:** drive turnouts on DCC accessory decoders by number ([79cc0a0](https://github.com/mikesmitty/interlock/commit/79cc0a0fd96e4dbea4b0e6b51c5aa1af7b9ecb33))
* **signals:** put signals on the track plan ([79aa8c8](https://github.com/mikesmitty/interlock/commit/79aa8c89224cd0d22d38d1fc712797e22836a1cb))
* **train:** hold trains at their speeds through the layout's emergency stop ([a7a1561](https://github.com/mikesmitty/interlock/commit/a7a156193c87b3a3daed666219a43802ff5cbd60))
* **train:** keep consists in the decoders ([bbd85b5](https://github.com/mikesmitty/interlock/commit/bbd85b55b88387b866671865c7008dc70b6178e4))
* **web:** add Peco Code 55 turnouts to the track plan editor ([167e447](https://github.com/mikesmitty/interlock/commit/167e4475032bf6a628af52840ca9a824d9cb3c38))
* **web:** group the navigation rail by task and open on the track plan ([9562110](https://github.com/mikesmitty/interlock/commit/956211046d5e8364a96ad925122c1907338e0c33))


### Bug Fixes

* **entities:** drop bi-color signal lamps ([62ba0b4](https://github.com/mikesmitty/interlock/commit/62ba0b4724d9222122d493823cf47569bd46c695))

## [0.2.0](https://github.com/mikesmitty/interlock/compare/v0.1.0...v0.2.0) (2026-10-28)


### Features

* **api:** serve decoder definitions, identify decoders and read or write CVs by name ([fe05057](https://github.com/mikesmitty/interlock/commit/fe0505748745ee143a6b6a3e072f19a164c63bf7))
* **decoder:** lay out ESU decoders' own function mapping ([0eb896c](https://github.com/mikesmitty/interlock/commit/0eb896c316d016a6ab3c8bf43a45d1442c332eec))
* **decoder:** load a family as a whole and read a decoder's address ([c1d3e1a](https://github.com/mikesmitty/interlock/commit/c1d3e1af3498f68af28dcd48da2d3de09cb94c81))
* **decoder:** read JMRI's decoder definitions from a folder, GitHub or the archive ([fa25659](https://github.com/mikesmitty/interlock/commit/fa256595ff6725105041c9f95bae497b8ece8241))
* **decoder:** resolve decoder definitions, identify decoders and address their CVs ([63d9be0](https://github.com/mikesmitty/interlock/commit/63d9be0acc7125b00db40f75ba308942d75c83ab))
* **driving:** drive trains to a stop at a stop marker ([2265087](https://github.com/mikesmitty/interlock/commit/226508719aad2a13646122c3398c4916a37dc178))
* **interlocking:** set routes, lock their turnouts and release them behind trains ([4ddfe25](https://github.com/mikesmitty/interlock/commit/4ddfe253891fd6adf6c66192a670dfc63e055ce7))
* **programmer:** read CVs on the main over RailCom ([0b7e67a](https://github.com/mikesmitty/interlock/commit/0b7e67a11e386137e243df9ec0d4cb8ef9e959f1))
* **release:** ship a Windows build and sign macOS and Windows builds ([2fe8df8](https://github.com/mikesmitty/interlock/commit/2fe8df804dee37c1a519e9851b5d6717435b5d1b))
* **replace:** move what names a node over to its replacement ([cc893d2](https://github.com/mikesmitty/interlock/commit/cc893d2d0991ec5566a9677e8f677affe38b9458))
* **roster:** cars and trains made up of locomotives and cars ([9a52fd3](https://github.com/mikesmitty/interlock/commit/9a52fd3727595b27fea39e579ce4f9d9d47f88ae))
* **roster:** check the decoder on the programming track before work on it ([79f813f](https://github.com/mikesmitty/interlock/commit/79f813fec6105a69aebeb2facbce0bd49b5f57f0))
* **roster:** keep changes not written yet with a decoder's CV values ([1d5e252](https://github.com/mikesmitty/interlock/commit/1d5e252f20ed796cc2bd6d242ba73e5eda0326b2))
* **roster:** keep each locomotive's decoders with their CV values and import a JMRI roster ([95c9fee](https://github.com/mikesmitty/interlock/commit/95c9fee866b13bdfac54b984e6809aaebaf30f14))
* **roster:** keep imported CV values apart until the decoder is read ([3d8c1dd](https://github.com/mikesmitty/interlock/commit/3d8c1dd62065070c236e921e43e7df5c66229e94))
* **roster:** keep named setups of a decoder's values to load again ([69e72a7](https://github.com/mikesmitty/interlock/commit/69e72a770912ea2f817d4befb57d41204b573355))
* **roster:** track a session of work on each decoder ([641939c](https://github.com/mikesmitty/interlock/commit/641939cf490c5126a9f09daeade0a3e4c2c80e99))
* run on Windows ([771befb](https://github.com/mikesmitty/interlock/commit/771befb1fb1b31112f4f78bdbf24d1a06dd5853f))
* **sim:** a simulated layout with trains running on the track plan ([94b050c](https://github.com/mikesmitty/interlock/commit/94b050c041ef50d22d9e40178b54252b8081e704))
* **sim:** simulate an ESU decoder with paged CVs on the programming track ([a537348](https://github.com/mikesmitty/interlock/commit/a53734888ab6244b9987c69e4958789a42c574ac))
* **tracking:** follow trains on the track plan from RailCom reports ([d4edc16](https://github.com/mikesmitty/interlock/commit/d4edc16db808ed39833447d0f2780e9f433c340c))
* **tracking:** measure a train's speed at each step it holds ([748e0c5](https://github.com/mikesmitty/interlock/commit/748e0c5f4529db766d0906c122f5158ab5592114))
* **trackplan:** correct a stray in a line without starting over ([2c62e35](https://github.com/mikesmitty/interlock/commit/2c62e351d808f94efe7cafe8ad6cbaa7a43fd4eb))
* **trackplan:** draw the track plan and work out its topology ([2b74186](https://github.com/mikesmitty/interlock/commit/2b74186ef863e262745a5cc3b71f6260dd20006e))
* **trackplan:** draw the track plan to scale and import RailModeller layouts ([23427dd](https://github.com/mikesmitty/interlock/commit/23427ddf9071979425227fab59cde016560d66be))
* **trackplan:** rotate the background image ([cdcd4fb](https://github.com/mikesmitty/interlock/commit/cdcd4fb0b32d7f074976d2b3f720dbb0479208b4))
* **trackplan:** select many, then move, turn or delete them together ([1b527a9](https://github.com/mikesmitty/interlock/commit/1b527a933839ebea50ee4834426ba9e967f97794))
* **trackplan:** trace a background image from planning software ([fae3435](https://github.com/mikesmitty/interlock/commit/fae343566d774ba853b4356ae787d6fec7e0a254))
* **trackplan:** zoom and move the plan in its canvas ([f08f2fe](https://github.com/mikesmitty/interlock/commit/f08f2fed8698347f05f78ca0852d7db87cf0d6a4))
* **train:** make up LCC consists on the command station ([7872ce6](https://github.com/mikesmitty/interlock/commit/7872ce600bb11c8976f03af7ee96cf811a83eea3))
* **train:** show track power as the command stations report it ([8efbcc2](https://github.com/mikesmitty/interlock/commit/8efbcc218e2eba2455396ac9af4ac7c61905ea82))
* **transport:** list Linux serial ports by their by-id names ([d05f461](https://github.com/mikesmitty/interlock/commit/d05f461dfce6b95a9cc89637e9fa9c8abd3b9a15))
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
* **entity:** ask detectors which locomotives they see ([7a15c9a](https://github.com/mikesmitty/interlock/commit/7a15c9a974d81d5d782c6eeb659f5128a688ff48))
* **memconfig:** reply to a requester known only by its alias ([5b83cae](https://github.com/mikesmitty/interlock/commit/5b83cae50ea784ab57e888092d8e4a6e9fe1a304))
* **nodeconfig:** show a node's new name as soon as it is written ([86884fd](https://github.com/mikesmitty/interlock/commit/86884fd6fc548a512c85a0aaaa9e04185de88724))
* **release:** stop release binaries reporting a modified tree ([8c7ddbb](https://github.com/mikesmitty/interlock/commit/8c7ddbba931cc6bb28df13af89bd42dc54e96cfd))
* **tracking:** list only trains a detector has seen ([1556460](https://github.com/mikesmitty/interlock/commit/1556460b3d35f27ec3efc458812d4683bb387f88))
* **trackplan:** draw diagonals freehand without a staircase ([f7515a9](https://github.com/mikesmitty/interlock/commit/f7515a955c7ca95d7ccb68484431085d764bddc8))
* **trackplan:** keep Look Up off the drawing grid ([8275dd1](https://github.com/mikesmitty/interlock/commit/8275dd133f8671134401e88cacd4e67c655e036b))
* **trackplan:** place Kato's 248 mm single crossovers, one double crossover ([12864eb](https://github.com/mikesmitty/interlock/commit/12864eb9ef24b53b5603f660d38a4420af9fa46f))
* **traction:** count only 06.01.00.00 node IDs as train nodes ([5a36583](https://github.com/mikesmitty/interlock/commit/5a36583be3475eedf2809658043dcc884d6119b8))
* **web:** pad the Decoder settings panel and line up the Programming page ([a9756e9](https://github.com/mikesmitty/interlock/commit/a9756e94159e850a6ef67ddae898ff7788dff796))
* **web:** widen the navigation rail so Programming and Automations fit ([2fbd8db](https://github.com/mikesmitty/interlock/commit/2fbd8dbff2666a3d666584f1138dccb4ec3d53af))


### Miscellaneous

* **docs:** set up Prettier ([a7835b5](https://github.com/mikesmitty/interlock/commit/a7835b5d37d556b930d4384082453b0178ad429c))
* have git blame skip the docs formatting commit ([9fd0f3d](https://github.com/mikesmitty/interlock/commit/9fd0f3d081fb66cedcc35891adf23c28e275f060))
* have git blame skip the Prettier formatting commit ([091794b](https://github.com/mikesmitty/interlock/commit/091794bf5c99d991b43c66b251c1c5d1eefd2068))
* remove a binary committed by mistake ([60b2b3e](https://github.com/mikesmitty/interlock/commit/60b2b3e255a9a821a7e7e8ccd46d26cd7269c7bc))
* **web:** set up Prettier ([1cbed1b](https://github.com/mikesmitty/interlock/commit/1cbed1b58aafd158ce540d38ce322ffa48d87164))


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
