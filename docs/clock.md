# The fast clock

Interlock runs the layout's fast clock and broadcasts it on the bus with
the LCC Broadcast Time protocol, so every node that keeps time (station
clocks, throttles, signal systems) shows the same time, and automations
can run to the timetable rather than the wall clock.

The Clock page shows the time, starts and stops it, sets its rate and
sets the time and date. The status strip shows the time on every page.
The clock's state is kept in the database, so Interlock comes back with
the clock where it was (stopped or running from that time).

## On the bus

Interlock produces the default fast clock, ID `01.01.00.00.01.00`. Its
events are that ID followed by two bytes:

| Suffix         | Meaning                                                          |
| -------------- | ---------------------------------------------------------------- |
| `0hmm`         | time report: hours and minutes in hex (`06 2A` is 06:42)         |
| `2MDD`         | date report: month and day                                       |
| `3YYY`         | year report                                                      |
| `4RRR`         | rate report, in quarters as a 12 bit signed number (`010` is 4×) |
| `F000`         | query: Interlock answers with its whole state                    |
| `F001`, `F002` | stop, start                                                      |
| `F003`         | date rollover: the next time report starts a new day             |

Setting the top bit of a report's suffix makes it a command: another node
sends `8hmm` to set the time, `A MDD` the date, `BYYY` the year, `CRRR`
the rate. Interlock applies these and reports the new value, and three
seconds after any change broadcasts its whole state (start or stop, rate,
year, date, time), as OpenMRN clocks do. It also broadcasts when asked
with a query and when a new clock consumer identifies itself.

A time report goes out every fast minute while the clock runs. Rates run
from one quarter of real time to several hundred times; negative rates
run the clock backward. Stopped, the clock still answers queries and
takes settings.

## In automations

A `clock` trigger fires when the fast clock reaches a time of day
(`at: "06:42"`), on the fast calendar's weekdays if listed. Setting the
clock to that time fires it too. A `clock` condition checks that the
fast time is in a window (`after`, `before`, `weekdays`). See
`docs/automations.md`.

## API

```
GET  /api/v1/clock          state: time, date, weekday, rate, running, updated
POST /api/v1/clock          {"running": true, "rate": 4, "time": "06:00", "date": "1957-06-21"}
```

Any subset of the fields may be sent. The `clock` server-sent event
carries the state after every change and every fast minute; between
events the time runs on from `updated` at `rate`.
