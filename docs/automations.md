# Automations

An automation watches for something (**when**), checks a few things
(**only if**) and then acts (**then**). The web interface builds them;
they are kept in `automations.yaml` in the data directory, which you can
also edit by hand (restart Interlock afterwards). Every run leaves a
trace: what started it, how each condition came out and what each action
did, with timing. Traces are the first place to look when an automation
does not do what you meant.

```yaml
automations:
  - id: close_the_yard_lead_behind_a_train
    name: Close the yard lead behind a train
    enabled: true
    mode: single
    triggers:
      - type: state
        entity: block.0a0000000042.track_2
        to: occupied
    conditions:
      - type: state
        entity: turnout.0a0000000042.servo_1
        state: thrown
    actions:
      - type: delay
        for: 3s
      - type: command
        entity: turnout.0a0000000042.servo_1
        command: close
```

Entity IDs are shown on the Entities page; durations are written like
`3s`, `1m30s`, `250ms`, or as a bare number of seconds; times of day are
`HH:MM` or `HH:MM:SS` in the server's local time; weekdays are `mon`
through `sun`.

## The automation

| Field | Meaning |
|---|---|
| `id` | Lowercase letters, digits and underscores. Made from the name when left out. |
| `name`, `description` | What it is for. |
| `enabled` | `false` turns it off; it can still be run by hand. Default `true`. |
| `mode` | What a new trigger does while a run is going: `single` (default) ignores it, `restart` stops the run and starts over, `queued` runs again afterwards, `parallel` runs alongside. |
| `max` | How many runs `queued` and `parallel` allow; default 10. |

## Triggers (when)

Any one trigger starts a run. A trigger may carry an `id` so a `trigger`
condition can tell which one fired; the id also shows in traces.

| `type` | Fields | Fires when |
|---|---|---|
| `state` | `entity`, `from`, `to`, `for` | The entity's state changes. `from` and `to` narrow it to one transition; `for` waits until the new state has held that long (a change back cancels it). An entity seen for the first time does not fire. |
| `locomotive` | `entity` (a reporter), `direction` (`entered`, `left`, or either), `address`, `address_type` (`long`, `short`, `consist`) | A reporter sees a locomotive arrive or leave. |
| `event` | `event` | That event ID is reported on the bus. |
| `node` | `node` (empty for any), `to` (`online`, `offline`, or either) | A node comes online or goes offline. |
| `time` | `at`, `weekdays` | That time of day. |
| `interval` | `every` | Every so often, counted from when Interlock started or the automation was saved. |
| `start` | | Interlock starts. |

When Interlock starts it learns the layout's states from the nodes; a
block found occupied changes from `unknown` to `occupied`, which fires a
`state` trigger with `to: occupied`. Set `from: clear` when that matters.

## Conditions (only if)

All conditions must hold when a run starts; otherwise the trace ends with
"conditions not met". Conditions are also used inside `if`, `choose`,
`repeat` and `wait`.

| `type` | Fields | Holds when |
|---|---|---|
| `state` | `entity`, `state` | The entity is in that state. |
| `time` | `after`, `before`, `weekdays` | The time of day is in the window (which wraps past midnight when `after` is later than `before`) and the day is listed. |
| `trigger` | `trigger` | The run was started by the trigger with that id. |
| `and`, `or` | `conditions` | All, or any, of the nested conditions hold. |
| `not` | `conditions` (one) | The nested condition does not hold. |

## Actions (then)

Actions run in order. An action that fails (a command to an offline
node, say) ends the run with an error. Any action can carry
`disabled: true` to be skipped, which is handy while debugging.

| `type` | Fields | Does |
|---|---|---|
| `command` | `entity`, `command` | Sends an entity's command: `throw`, `close`, `center`, `on`, `off`, `press`. |
| `event` | `event` | Produces that event from Interlock's node. |
| `delay` | `for` | Waits. |
| `wait` | `triggers`, `timeout`, `stop_on_timeout` | Waits for one of the triggers (any type but `start`, `time` and `interval`). After `timeout` the run goes on, or stops when `stop_on_timeout` is set. |
| `if` | `conditions`, `then`, `else` | Runs `then` when the conditions hold, else `else`. |
| `choose` | `choices` (each `name`, `conditions`, `actions`), `default` | Runs the first choice whose conditions hold, else `default`. |
| `repeat` | one of `count`, `while`, `until`; `actions` | Repeats the actions a number of times, while conditions hold (checked first), or until they hold (checked after). At most 1000 times. |
| `stop` | `reason`, `error` | Ends the run; `error: true` marks it failed. |
| `log` | `message` | Writes to Interlock's log and the trace. |

## Traces

The last 20 runs of each automation are kept in the database. A trace
records the trigger, every condition and action with a path such as
`actions[1].then[0]`, when each started and ended, and how it came out:
done, not met, skipped, failed, timed out or canceled. Runs by hand skip
the conditions unless asked otherwise. A run is canceled when the
automation is turned off, deleted, stopped by hand, restarted by a new
trigger in `restart` mode, or when Interlock shuts down.

## Not yet

A fast clock, numeric comparisons, variables and templates. The document
shape leaves room for an expression language in conditions later.
