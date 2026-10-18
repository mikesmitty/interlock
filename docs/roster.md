# The roster

The Roster page lists your locomotives. Each one has a name, the DCC
address the throttle drives, function labels, what the locomotive is (road
name and number, manufacturer, model, owner, notes) and its decoders.

## Decoders

A locomotive can have more than one decoder: the one fitted now, and any
you swap in and out of it. Each decoder has a definition (the model, from
the [decoder definitions](./decoders.md)), the identity it reported when
it was identified, and the CV values last read from or written to it.

One decoder is marked as fitted. Its address and function labels are the
locomotive's: the throttle drives that address, and writing a new address
to the decoder on its settings page moves the locomotive with it. When you
mark another decoder as fitted, the one coming out keeps the address and
labels it had, and the one going in brings back its own. So a locomotive
that runs with a sound decoder one week and a test decoder the next keeps
the right address and function names for each.

**Settings** on a decoder opens its settings page with the saved values
already in place, so nothing has to be read before you look. Every value
read or written there is saved for that decoder, and changes not written
yet are kept until they are written or discarded. A page shows when the
values were last saved; read it again if the decoder may have been
changed elsewhere since. Values read or written in the current
[session](./decoders.md#sessions) carry green lamps; older ones have no
lamp.

To add a decoder, choose **Add a decoder** and identify it on the
programming track or search for the model. A decoder opened from the
Programming page on its own can be kept too: **Keep in the roster** on its
settings page adds it to a new or existing locomotive with the values read
so far.

## Which locomotive is on the track

**Identify the decoder** on the Programming page also reads the address
the decoder answers to, then looks in the roster. A decoder at the same
address whose saved identity matches is named first, with a link to its
settings; a decoder that is saved but not marked as fitted can be marked
from there. A locomotive at that address with no decoder yet can take the
definition you pick.

## Several locomotives at one address

Locomotives may share an address, such as two that never run at the
same time. To the command station they are one train: driving either one
drives whichever locomotive is on the track at that address, and the
throttle shows the same speed for both. An automation that drives the
address lists both names in its trace.

## Importing a JMRI roster

**Import from JMRI** reads the roster JMRI keeps in its profile folder:
`roster.xml` and the locomotive files in the `roster` folder beside it
(on Linux, `~/.jmri/<profile>/roster`). Choose those files, or a zip of
the whole profile folder. Interlock shows what it found before anything
changes:

- Each locomotive comes in with its name, address, function labels
  (functions JMRI marks as not latching become momentary), road name and
  number, manufacturer, model, owner and comment.
- Its decoder comes in with the definition JMRI used and every CV value
  the locomotive file holds. Those values are marked as imported, not
  read: the settings page shows them but counts them as not read yet
  until a read confirms them. A change to part of a CV (one bit of CV
  29, say) reads the decoder before writing, so its other bits are what
  the decoder holds, and **Write page** asks before writing imported
  values over what the decoder holds. A decoder model that the definitions no longer have
  is marked; choose its definition later on the locomotive's page.
- Entries with the same address, road name and road number are offered
  as one locomotive with several decoders: the most recently changed
  one is fitted, and the others join it, named by what their names add
  ("Amtrak P42 ESU" and "Amtrak P42 Digitrax" become Amtrak P42 with an
  ESU and a Digitrax decoder).
- A locomotive already in the roster under the same name is left out.

Each row can be changed before importing: a locomotive of its own, a
decoder of another locomotive (one in this import or in the roster), or
left out. Names can be changed too. A locomotive listed in `roster.xml`
without its own file comes in without CV values.

## The files

The roster is `trains.yaml` in the data directory; see
[Trains](./trains.md#the-roster) for its fields. Each decoder's CV values
are in `cvs/<decoder id>.yaml`, with imported values not read yet and
the changes not written yet, which Interlock rewrites a second after
values stop changing. Both are in [backups](./backup.md).
