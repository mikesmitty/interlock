# Backup and restore

Everything Interlock keeps lives in its data directory: `interlock.yaml`,
`entities.yaml`, `automations.yaml`, `trains.yaml`, `trackplan.yaml` and its
background image in `backgrounds/` (and `trackplan-grid.yaml`, a plan
drawn on the old grid as it was before it was converted), the roster's decoder
CV values in `cvs/`, your `profiles/` and the database (`interlock.db`, which holds the nodes it has seen, their
configuration descriptions, the named event table, automation traces,
the fast clock and Interlock's own generated node ID).

## Backing up

The Settings page has "Download a backup": one zip file with all of the
above and a manifest naming the Interlock version that wrote it. The
database goes in as a consistent snapshot, so the backup is good even
while Interlock is busy. Keep the file wherever you keep such things; it
is also what you move to a new machine.

```
GET /api/v1/backup
```

does the same, for a scheduled job.

## Restoring

Choose the file under "Restore from a backup" on the Settings page and
confirm. Interlock checks that the file is one of its own backups and
holds only files it keeps, stages it next to the data, and restarts. On
the way back up it moves the files being replaced into a
`before-restore-<time>` folder in the data directory, puts the backup's
files in place and starts as usual. Files the backup does not hold (one
the newer Interlock made, say) are left alone.

A restore replaces `interlock.yaml` too. If the backup came from another
machine, the serial device names and addresses in it may need editing
afterwards.

## Restarting

"Restart Interlock" on the Settings page stops everything cleanly (the
node withdraws its alias, automations are canceled and their traces say
so) and starts again in the same process, reading `interlock.yaml`
fresh. It is the way to pick up a change to the file without touching
the service.
