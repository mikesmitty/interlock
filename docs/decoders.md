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
  directory. The first look at a decoder fetches its files several at a
  time, which takes under a second; after that it needs no connection.
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

- **Read page** reads every CV the tab shows. Changes not written yet
  stay; the rest of their CV comes from the decoder.
- **Write changes** writes only the CVs you changed. A setting that
  shares its CV with others is written without disturbing them: unless
  that CV was read or written in this session, Interlock reads it first.
- **Write page** writes every CV on the tab that has a value, for setting
  up a new decoder from the defaults or putting a saved copy back. When
  some of those values were not read from the decoder in this session
  (values saved earlier, values imported from a JMRI roster, or
  defaults), it asks first, since they replace whatever the decoder
  holds.
- **Discard changes** puts the tab's changed settings back as they were.

Changed settings carry a brass mark until they are written, and so do
the tabs that have them. Reads and writes go one CV at a time; the
toolbar shows how far it has got and **Stop** ends it after the current
CV.

A lamp in front of each setting says where its value came from, and the
key at the top of each tab repeats it:

| Lamp | Means |
|---|---|
| Green | Read from the decoder in this session |
| Green ring | Written to the decoder in this session, not read back |
| Amber | Imported from a JMRI roster, not read since |
| Amber ring | The definition's default, not read |
| Unlit | Not read, and no value to show |
| None | Saved from before this session |

A setting that spans several CVs shows the least certain of them. The
ESU function map's rows and the CVs tab carry the same lamps.

### Sessions

For a decoder in the roster, a session is a spell of work on it, shown
above the tabs with an **End session** button. On the programming track
it begins with a check that the decoder there is the one you opened;
on the main, where the address picks the decoder, with the first write.
Its values keep their green lamps when the page is reloaded or opened on
another device. A session ends:

- after two hours without a read, a write or a change on the page;
- when **Identify** finds a decoder on the programming track, since the
  locomotive there may have been swapped or changed elsewhere;
- when another decoder is checked on the programming track;
- with **End session**;
- when Interlock restarts.

Its values stay saved and show without a lamp from then on. Only values
read or written in the session are trusted to be what the decoder holds
now, since it may have been changed elsewhere in between: a change to
part of a CV saved earlier reads the decoder first, and **Write page**
asks before writing saved values. A decoder opened on its own, outside
the roster, keeps its lamps until the page closes.

### Checking the decoder on the track

A fleet often has many of the same decoder, so before the first read or
write of a session on the programming track, Interlock reads the
decoder's manufacturer, product ID and address (the CVs Identify reads,
which take ten to twenty seconds on a slow command station) and compares
them with the roster:

- the manufacturer and product ID with those saved for the decoder, or
  with its definition's when none were saved;
- the address with the locomotive's;
- the CVs read on the way, such as CV 29, with the values saved or
  imported for the decoder.

When another roster decoder has the same model at the same address, such
as a pair of units that run as one, Interlock also reads up to three CVs
whose saved values tell the two apart.

If everything agrees, the session begins, the CVs read for the check
count as read, and the work goes on. If anything differs, nothing is read
or written: the page says what differs and which roster decoders the one
on the track could be. **Read anyway** or **Write anyway** begins the
session regardless, for a new decoder that still answers at address 3
for instance, and the session line then says the decoder did not match.

**Identify** checks the roster decoder it names first the same way and,
when it agrees, begins that decoder's session, so its settings open
ready to read. Interlock cannot see a locomotive being lifted off the
track: after swapping one, use Identify or **End session**.

### Decoder profiles

Interlock saves each value as it reads or writes it, so the roster
always has what the decoder last held. To keep a known-good state to
come back to, such as how a decoder came or how it ran best, name it
under **Decoder profiles** at the bottom of the page and choose **Keep
these values**. A decoder profile copies the values saved for the
decoder, imported ones included; changes not written yet are left out. A
name kept already is replaced only after you say so.

**Load** marks every value that differs from the profile as a change,
and drops changes not written yet that the profile undoes. Values that
already match the profile but were not read from the decoder in this
session are left alone, though the decoder may hold something else, and
the panel says how many there are: read their pages and load the profile
again to find any that differ, or choose **Mark them too** to write them
as well, for a full restore. Nothing reaches the decoder until you write
the changes, page by page or with **Write changes on every page**. Loading over changes not written yet
asks first. Decoder profiles live with the decoder's values in the data
folder, and backups include them. They are not the node
[profiles](./entities.md) that turn LCC boards into entities.

Changes to a decoder in the [roster](./roster.md) are kept until they
are written or discarded: close the page, or open it on another device,
and they are still there, marked. A decoder opened on its own keeps
nothing: the browser asks before you close or reload its page with
changes, and **Keep in the roster** takes them along.

The address field takes the address you want: 1 to 127 is a short
address, anything higher a long one, and CV 29 is set to match.

Many decoders keep more settings than CVs 1 to 1024 can hold and reach
them through index CVs (CV 31 and 32 on ESU decoders). The definition
says how; Interlock writes the index once for a run of CVs behind the
same index rather than before every one.

To program on the main, choose the locomotive under **Program**. Writes
go out as programming-on-the-main packets; reading on the main needs
RailCom, which is not available yet.

## ESU function mapping

ESU decoders decide what each function key does with a table of their
own rather than the usual function map: 72 rows on a LokSound 5 (40 on
earlier ones), each switching some outputs, logical functions (such as
shunting mode or momentum off) and sounds on while all of its
conditions hold. A condition is a function key on or off, moving or
stopped, forward or reverse, or a sensor.

The Function Map tab lists the rows with what each one does: "Forward,
F0" and "Headlight [1]" is the headlight in forward with F0 on. Choose a
row's number to change it: set each condition to either, on or off, and
tick what it switches on. Outputs and sound slots the model lacks are
left out, and function keys show their roster labels. **Read row** reads
just that row, much quicker than the whole table, and **Clear row**
empties it. Outputs marked [2] are the second configuration of the
headlight, rear light, AUX1 and AUX2.

## With the roster

A decoder in the [roster](./roster.md) opens from its locomotive's page
with the values saved for it, so a page shows the decoder's settings
without reading them first, marked as saved. Values imported from a JMRI
roster show too, but count as not read until a read confirms them, and a
change to part of an imported CV reads the decoder before it is written.
Each value read or written is saved again, and writing a new address to
the fitted decoder moves the locomotive to it. A decoder opened on its own can be kept with a
locomotive from **Keep in the roster** at the bottom of its page.

## Without hardware

`go run ./cmd/simnode -cdi cs -id 0A.00.00.00.00.43 -decoder esu -address 6641`
simulates a command station with an ESU LokSound 5 micro DCC Direct on
its programming track. It identifies itself as one and keeps its high CVs
in pages behind CV 31 and 32, as the real decoder does.
