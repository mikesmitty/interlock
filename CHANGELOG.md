# Changelog

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
