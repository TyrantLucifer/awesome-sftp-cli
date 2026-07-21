# Vim-first Keymap Reference

AMSFTP translates terminal input into typed actions before the reducer handles navigation, selection, confirmation, or Job creation. Remapping changes only this translation. It cannot bypass counts, dot-repeat freezing, Visual selection, destructive confirmation, conflict policy, or the durable Planner/Job/Worker mutation path.

## Contexts and configuration

Schema version 1 supports `normal` and `visual` keymap contexts. `visual` inherits the complete Normal default map until an action is explicitly remapped in that context. A remap moves one remappable action from its old input to one new single-rune input; it does not create a second binding.

```json
{
  "schema_version": 1,
  "keymap": {
    "bindings": [
      {"context": "visual", "input": "n", "action": "down"}
    ]
  }
}
```

`amsftp config validate` rejects unknown contexts/actions, duplicate action overrides, inputs already owned by another action, digits reserved for count prefixes, control/multi-rune inputs, dangerous reserved actions, and reserved sequence inputs. `amsftp config print-effective-keymap [<path>]` exports the complete versioned Normal/Visual map, including moved inputs and their defaults. `amsftp config reset-keymap --yes [<path>]` atomically clears only validated overrides and preserves every unrelated configuration value. Removing `bindings` manually has the same semantic effect; the unchanged default map is also covered by an exact snapshot test.

## Default bindings

| Input | Action | Remap policy |
|---|---|---|
| `h` / `j` / `k` / `l` | parent / down / up / open | remappable |
| `←` / `↓` / `↑` / `→` | parent / down / up / open | fixed physical-key aliases in Normal and Visual modes |
| `v` / `V` / Space | Visual / Visual Line / discrete mark | remappable |
| `/` / `f` | fuzzy current-directory jump / recursive filename search | remappable |
| `S` / `s` / `H` / `R` | save workspace / sort / hidden / refresh | remappable |
| `c` | open the fuzzy Endpoint picker for `local` and discovered OpenSSH Hosts | remappable; unmatched text is not submitted |
| `y` / `d` / `p` / `r` | copy / cut / paste / rename | remappable; original confirmation and Job rules remain |
| `e` / `o` / `E` | edit / open externally / edit recovery | remappable |
| `K` / `J` / `L` | Preview / Jobs / Log drawer | remappable |
| `g` then `s` / `S` | shell at current directory / explicit home fallback | reserved sequence |
| `D` | actual delete | reserved dangerous action; still requires two confirmations |
| `!` | one-time command | reserved dangerous action; still requires explicit confirmation |
| `.` | repeat last frozen operation | reserved; cannot weaken confirmation |
| `P` / `U` / `C` | pause / resume / cancel Job | reserved control actions |
| `w` / `x` / `a` | overwrite / skip / auto-rename conflict | reserved decision actions |
| `W` / `X` / `A` | apply conflict decision to all | reserved decision actions |

Digits remain count prefixes before keymap lookup. Counts are accepted only for the existing bounded navigation/copy/cut/paste/delete/rename set. Unsupported count/action combinations are ignored safely. Arrow keys are fixed physical-key aliases for the four navigation actions and do not change or consume configurable single-rune bindings. `q` remains the fixed Normal-mode quit input and cannot be a Normal remap target; it remains available to an explicit Visual-context remap. Visual mappings use the `visual` context; confirmation, text-entry, authentication, recovery, and drawer-specific keys retain their fixed safe behavior.

## Deliberate 1.0 exclusions

AMSFTP 1.0 does not implement Vim macros or named registers. There are no `macro_record`, `macro_play`, or named-register actions in the schema or default map; unknown attempts fail validation. Clipboard operations use the single durable AMSFTP clipboard and repeat only replays already frozen operations under their original confirmation rules.
