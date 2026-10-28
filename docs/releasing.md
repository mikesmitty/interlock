# Releasing

Releases are cut by [release-please](https://github.com/googleapis/release-please).
Every merge to `main` updates an open release pull request with the next
version and its changelog, worked out from the conventional commit
messages. Merging that pull request tags the release, and the Binaries
workflow attaches the builds to it:

| Platform                         | File                                       |
| -------------------------------- | ------------------------------------------ |
| Linux on amd64, arm64 and arm    | `interlock-<version>-linux-<arch>.tar.gz`  |
| macOS on Apple silicon and Intel | `interlock-<version>-darwin-<arch>.tar.gz` |
| Windows on x64                   | `interlock-<version>-windows-amd64.zip`    |

A checksums file lists the SHA-256 of each. Never bump versions by hand.

To try the build without releasing anything, open the Binaries workflow on
the repository's Actions tab and choose **Run workflow**. The dry run
keeps the archives as a workflow artifact named `release-files`.

## Signing

macOS and Windows warn about programs downloaded in a browser unless the
program is signed by an identified developer. macOS refuses to open an
unnotarized download until its quarantine flag is cleared, and Windows
SmartScreen shows "Windows protected your PC". The Binaries workflow signs
each platform's build once that platform's credentials are in the
repository settings. Until then the build ships unsigned and
[Getting started](./getting-started.md#install) tells people how to run
it anyway. A dry run shows whether new credentials work before a release
depends on them.

### macOS: Developer ID and notarization

This needs a membership in the [Apple Developer
Program](https://developer.apple.com/programs/), which costs $99 a year.

1. Create a **Developer ID Application** certificate in the developer
   account or in Xcode's account settings. Export it from Keychain Access
   as a `.p12` file with a password.
2. In App Store Connect, under Users and Access, then Integrations, create
   a team API key with Developer access and download its `.p8` file.
3. Add these repository secrets:

| Secret                       | Value                                           |
| ---------------------------- | ----------------------------------------------- |
| `APPLE_CERTIFICATE`          | the `.p12` file in base64: `base64 -i cert.p12` |
| `APPLE_CERTIFICATE_PASSWORD` | the `.p12` file's password                      |
| `APPLE_NOTARY_KEY`           | the contents of the `.p8` file                  |
| `APPLE_NOTARY_KEY_ID`        | the API key's ID                                |
| `APPLE_NOTARY_ISSUER`        | the issuer ID shown above the list of keys      |

The workflow signs both macOS binaries with the hardened runtime and
sends them to Apple's notary service. A bare executable cannot carry the
notarization ticket, so macOS checks with Apple online the first time the
program runs.

### Windows: Azure Artifact Signing

This needs an Azure subscription and an [Artifact
Signing](https://learn.microsoft.com/azure/artifact-signing/) account,
which bills monthly.

1. Create an Artifact Signing account, complete identity validation for
   public trust, and create a certificate profile.
2. Create an app registration with a client secret, and give it the
   **Artifact Signing Certificate Profile Signer** role on the account.
3. Add these repository secrets and variables:

| Secret                | Value                            |
| --------------------- | -------------------------------- |
| `AZURE_TENANT_ID`     | the directory (tenant) ID        |
| `AZURE_CLIENT_ID`     | the app registration's client ID |
| `AZURE_CLIENT_SECRET` | the client secret                |

| Variable                    | Value                                                                       |
| --------------------------- | --------------------------------------------------------------------------- |
| `AZURE_SIGNING_ENDPOINT`    | the account's region endpoint, such as `https://eus.codesigning.azure.net/` |
| `AZURE_SIGNING_ACCOUNT`     | the Artifact Signing account name                                           |
| `AZURE_CERTIFICATE_PROFILE` | the certificate profile name                                                |

A newly signed program can still meet the SmartScreen prompt until
enough people have run it, but the prompt names you as the publisher
instead of "Unknown publisher". [SignPath
Foundation](https://signpath.org/) signs open source projects for free
after an application, and would replace the Azure step in the workflow.
