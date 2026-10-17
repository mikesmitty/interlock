# Decoder settings

The Programming page can change a DCC decoder's settings by name instead
of by CV number: acceleration, the address, how each function output
behaves, sound volumes. The names, ranges and choices come from the
decoder definitions the JMRI project maintains for some 3,800 decoder
models. Interlock reads those files; it does not include them.

## Where the definitions come from

The definitions are licensed separately from Interlock, so they are
fetched rather than built in. The `decoders` setting in `interlock.yaml`
picks the source:

```yaml
decoders:
  source: github      # github, archive or folder
  ref: v5.16          # the JMRI release, for github and archive
  # path: /home/pi/JMRI
```

- **github** (the default) fetches each file from JMRI's repository the
  first time it is needed and keeps it in `decoders/` in the data
  directory. The first look at a decoder takes a few seconds; after that
  it needs no connection.
- **archive** downloads the whole repository at that release once, in
  the background, and keeps only the decoder files (about 75 MB of a
  365 MB download). Until it finishes, files are fetched one at a time as
  with `github`.
- **folder** reads a JMRI install, a checkout of the JMRI repository, or
  a zip of either, from `path`. Use it on a machine with no internet
  connection.

Backups leave the fetched files out; they can always be fetched again.

## Finding the decoder

Put the locomotive on the programming track and choose **Identify the
decoder**. Interlock reads the manufacturer (CV 8) and version (CV 7),
then the product ID for makers that publish one (ESU, TCS, Zimo,
SoundTraxx and others), and lists the definitions that fit. It also
reads the address the decoder answers to and names the roster
locomotive whose decoder it is, when there is one. Some
products share an ID: an ESU LokSound 5 and LokPilot 5 micro DCC Direct
report the same one, so both are offered and you choose the one fitted.

The search box finds any model by manufacturer, family or name, for a
decoder that cannot be identified or one you want to look at first.

## Changing settings

The settings page has a tab for each page of the definition: Basic,
Motor, Speed Table, Function Outputs and so on, with each decoder's own
pages after the standard ones. Values start at the definition's
defaults, marked as not read yet.

- **Read page** reads every CV the tab shows.
- **Write changes** writes only the CVs you changed. A setting that
  shares its CV with others is written without disturbing them: if that
  CV was never read, Interlock reads it first.
- **Write page** writes every CV on the tab that has a value, for setting
  up a new decoder from the defaults.

Changed settings carry a brass mark until they are written. Reads and
writes go one CV at a time; the toolbar shows how far it has got and
**Stop** ends it after the current CV.

The address field takes the address you want: 1 to 127 is a short
address, anything higher a long one, and CV 29 is set to match.

Many decoders keep more settings than CVs 1 to 1024 can hold and reach
them through index CVs (CV 31 and 32 on ESU decoders). The definition
says how; Interlock writes the index once for a run of CVs behind the
same index rather than before every one.

To program on the main, choose the locomotive under **Program**. Writes
go out as programming-on-the-main packets; reading on the main needs
RailCom, which is not available yet.

## With the roster

A decoder in the [roster](./roster.md) opens from its locomotive's page
with the values saved for it, so a page shows the decoder's settings
without reading them first, marked as saved. Each value read or written
is saved again, and writing a new address to the fitted decoder moves
the locomotive to it. A decoder opened on its own can be kept with a
locomotive from **Keep in the roster** at the bottom of its page.

## Not done yet

- ESU decoders map functions to outputs and sounds in a table of their
  own, which the Function Map tab does not show yet. The Function Outputs
  and Function Settings tabs and the CVs tab cover the rest.
- Settings changed but not written are not kept; write them, or they
  are gone when the page closes.

## Without hardware

`go run ./cmd/simnode -cdi cs -id 0A.00.00.00.00.43 -decoder esu -address 6641`
simulates a command station with an ESU LokSound 5 micro DCC Direct on
its programming track. It identifies itself as one and keeps its high CVs
in pages behind CV 31 and 32, as the real decoder does.
