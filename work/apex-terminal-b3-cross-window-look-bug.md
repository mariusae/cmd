# Apex: terminal B3 falls back to `Look` in an unrelated text window

## Status

Confirmed on 2026-09-10. This is not leakage from Smartlog's window-scoped plumbing rule. When no plumbing rule accepts a B3 from a terminal, Apex's fallback `Look` searches `node.seltext`, which can refer to an unrelated text window. In this reproduction, launching Smartlog made the Smartlog body `seltext`, so B3 on a `D…` token in Newterm selected and revealed the same token in Smartlog.

## User-visible behavior

### Expected

B3 on a token in Newterm should not navigate an unrelated window merely because that window was most recently selected or opened. In particular, a Smartlog plumbing rule scoped to the Smartlog window must not affect B3 in Newterm.

### Actual

With Smartlog open, B3 on a Phabricator identifier such as `D117573677` in Newterm navigates to and selects that identifier in the Smartlog window, as though `Look` had been invoked there.

The behavior persists when Smartlog is freshly launched and its rules are correctly scoped to its own window.

## Environment and provenance

Observation time: `2026-09-10T13:05:40-07:00` (`America/Los_Angeles`).

| Item | Value |
| --- | --- |
| Apex session | `57f550de-0840-4e74-93b1-361aa69c67d4` |
| Apex socket | `/tmp/apex-meriksen/main.sock` |
| Installed Apex | `apex build dd312cd11664 protocol 14` |
| Apex source checkout | `/home/meriksen/src/tries/2026-09-07-mariusae-apex` |
| Apex source HEAD | `aeb40c7c29c68f9ca3abff79e4a4b1a0b951f766` |
| Smartlog source checkout | `/data/users/meriksen/fbsource/users/me/meriksen/Smartlog` |
| fbsource working-copy commit | `9d4e0a317c64665651c0e8fd80849a4ccef9c077` |
| fbsource commit date | `2026-09-10 12:48 -0700` |
| fbsource commit title | `[chrysalis] monomorphize QUIC packet send slots` |
| Smartlog executable | `/home/meriksen/bin/Smartlog` |
| Smartlog distribution | DotSlash/Everstore handle `GA8hpi9-zDPBO5YDAIzNP30esyQPbsIXAAAz` |

The Smartlog checkout was clean when inspected. The Apex source checkout contained unrelated, pre-existing XDG plumbing edits. None of the four files used for the control-flow analysis—`apex-client/src/app.rs`, `apex-core/src/plumb.rs`, `apex-server/src/lib.rs`, and `apex-server/src/proposal.rs`—was modified.

The installed Apex build ID is not present in the local Apex Git checkout, so the source trace below is against source HEAD `aeb40c7…`, not a byte-for-byte reconstruction of build `dd312cd…`. The live rule/window state and observed selection independently establish the runtime behavior.

## Reproduction

1. Start in a Newterm associated with the Smartlog checkout.
2. Launch Smartlog with debug logging:

   ```console
   $ echo $winid
   $ APEX_SMARTLOG_DEBUG=1 Smartlog
   Smartlog: /data/users/meriksen/fbsource in window w8796093022285
   ```

   The empty `$winid` is recorded as observed; the Apex UI still identifies the terminal window internally.

3. Confirm the relevant windows:

   ```text
   8796093022231  >/data/users/meriksen/fbsource/users/me/meriksen/Smartlog/-fbsource
   8796093022285  >/data/users/meriksen/fbsource/+smartlog
   ```

   A bridge inspection identified `8796093022231` as `kind: "term"`. `apex ps` also associated the process running in that terminal with window `8796093022231`.

4. Confirm the freshly installed Smartlog rule:

   ```text
   r44  smartlog-2235155(a117)  p100  -text='D[0-9]+' -win=8796093022285 -tool=smartlog-2235155
   ```

5. B3 `D117573677` in Newterm window `8796093022231`.
6. Observe that Apex reveals Smartlog and selects the D-number there.

Post-event inspection captured the resulting selection:

```console
$ apex sel 8796093022285
64 74
$ apex text read -addr='#64,#74' 8796093022285
D117573677
```

This selection is the direct observable side effect of Apex's fallback `Look`. Smartlog's `phab_diff` handler does not select text in Smartlog; it acknowledges the event and plumbs a Phabricator URL.

## Initial misleading observation

Before the clean reproduction, an older Smartlog process had the following rule:

```text
r28  smartlog-777074(a99)  p100  -text='D[0-9]+' -win=8796093022231 -tool=smartlog-777074
```

Because `8796093022231` was the Newterm window, this initially suggested that Smartlog had registered its rules against the wrong window. After restarting Smartlog with debug logging, Smartlog reported the correct new window ID (`8796093022285`), and the replacement rule `r44` was scoped to that ID. The user-visible behavior still reproduced. Therefore the stale `r28` ownership anomaly is not required for this bug and should be treated as a separate observation unless it can be reproduced independently.

## End-to-end control flow

### 1. The client sends B3 with the terminal window as its context

For terminal content, the client derives the word or link under B3 and calls:

```rust
self.look(ExecCtx::Window(w), &text)
```

Source: `crates/apex-client/src/app.rs:2014-2028`.

Here, `w` is the terminal window (`8796093022231`), not the Smartlog window.

Terminal B1 selection is client-local (`term_sel`) and only updates the active column; it does not update the core node's `seltext`:

```rust
self.term_sel = Some((w, p, p));
self.mouse.term_drag = Some(w);
self.node.activecol = self.column_of_view(ViewId::Tag(w));
```

Source: `crates/apex-client/src/app.rs:1860-1863`.

### 2. Smartlog's rule is correctly window-scoped

Smartlog installs every rule with its own window ID:

```rust
let mut offer = |rule: Rule| {
    apex.offer(rule.window(window).priority(100))
};
```

The D-number rule is:

```rust
phab_diff: offer(Rule::plumb().text(DIFF_PATTERN))?,
```

Source: `Smartlog/src/lib.rs:660-675`.

The Apex matcher strictly rejects a rule whose `win` differs from the plumbing context:

```rust
if let Some(id) = self.win {
    if w != Some(id) {
        return false;
    }
}
```

Source: `crates/apex-core/src/plumb.rs:82-87`.

Smartlog also independently rejects any delivered event whose window does not equal its own:

```rust
if plumb.window != Some(self.window) {
    return self.apex.answer(&plumb, false);
}
```

Source: `Smartlog/src/lib.rs:803-808`.

Consequently, `r44` cannot accept a plumb from terminal window `8796093022231`; it requires Smartlog window `8796093022285`.

### 3. With no matching rule, plumbing deliberately falls back to `Look`

After exhausting plumbing rules, the server returns:

```rust
Proposal::Look { ctx, text, reverse }
```

Source: `crates/apex-server/src/lib.rs:1306-1314`.

The original `ctx` still identifies the terminal window.

### 4. `Proposal::Look` ignores that origin while any valid `seltext` exists

The proposal handler chooses the search view in this order:

```rust
let view = node
    .seltext
    .filter(|v| node.view_buffer(*v).is_ok())
    .or_else(|| match ctx {
        ExecCtx::Window(w) => Some(ViewId::Body(w)),
        ExecCtx::Column(c) => Some(ViewId::ColTag(c)),
        ExecCtx::Top => Some(ViewId::Top),
    });
```

It then searches, reveals the selected window, and requests a mouse warp:

```rust
if node.view_buffer(v).is_ok() && node.look_dir(log, v, &text, reverse)? {
    if let Some(w) = v.window() {
        node.reveal(log, w)?;
    }
    node.warp = Some(Warp::Sel(v));
}
```

Source: `crates/apex-server/src/proposal.rs:288-306`.

The source comment describes this as Acme behavior: the search runs in the text last selected with B1, not necessarily where B3 was clicked. In this case that rule crosses a window boundary in a surprising way: a B3 originating in a terminal searches an unrelated file-like window.

### 5. Launching Smartlog makes its body the remembered `seltext`

Smartlog creates or replaces its window and then invokes:

```rust
apex.open(&name, current_line(&text))?;
```

Source: `Smartlog/src/lib.rs:111-126`.

`ApexTool::open` proposes a `Goto` (`crates/apex-tool/src/lib.rs:411-421`). Applying `Goto` calls `Node::land` (`crates/apex-server/src/proposal.rs:250-261`), and `Node::land` explicitly records the destination body as `seltext`:

```rust
self.reveal(log, w)?;
self.seltext = Some(ViewId::Body(w));
self.warp = Some(Warp::Sel(ViewId::Body(w)));
```

Source: `crates/apex-core/src/node.rs:1266-1275`.

Because terminal B1 does not replace core `seltext`, Smartlog remains the fallback search target while the user interacts with Newterm.

## Root cause

`Proposal::Look` unconditionally prefers the global `node.seltext` over the B3 request's `ctx`. For terminal-originated B3, the terminal selection is maintained only by the client and cannot displace that global text-view selection. When plumbing finds no rule, the fallback therefore searches and reveals whichever unrelated text window remains in `seltext`.

Smartlog makes the issue especially deterministic because opening it establishes its body as `seltext`, and its rendered body contains the same D-numbers users commonly B3 in terminals.

## Why this is not the Smartlog rule

- Fresh Smartlog debug output and `apex plumb rule ls` agree on window `8796093022285`.
- The click originates in terminal window `8796093022231`.
- Apex's rule matcher enforces exact window equality.
- Smartlog performs a second exact-window check.
- Smartlog's matching `phab_diff` branch opens a URL; it does not select/reveal text in its own window.
- The observed selection of `D117573677` in Smartlog exactly matches the side effects of `Proposal::Look`.

## Diagnostic limitation

`apex plumb -dry-run TEXT` cannot currently simulate a particular window. The CLI always submits dry runs with `ExecCtx::Top` (`crates/apex-cli/src/main.rs:1222-1241`), and it has no `-win` flag. Setting the shell variable `winid` therefore does not change a dry run's context. A UI-level test or an in-process server test is needed to exercise the exact terminal context.

## Suggested fix boundary

The narrow fix should be in fallback-`Look` target selection, not in Smartlog and not in plumbing-rule matching.

For a `Look` originating from a terminal, Apex should not reuse `seltext` from an unrelated window. The intended terminal behavior needs an explicit choice:

- do nothing when terminal plumbing finds no rule;
- search only if there is a meaningful source-local target; or
- retain global Acme-style `seltext` behavior only when the source context is itself a text view.

The third option most directly preserves the existing behavior for ordinary text windows while preventing terminal B3 from jumping elsewhere.

Changing Smartlog's reveal operation from `open`/`Goto` to `show` would avoid making Smartlog the `seltext` target during startup and would mitigate this particular reproduction. It would not fix the underlying cross-window fallback: any other stale text view could still become the terminal's search target.

## Suggested regression test

Add an integration test covering the entire fallback path:

1. Create text window A containing `D117573677`.
2. Make A the node's `seltext` through the normal `Goto`/`land` path.
3. Create terminal window B.
4. Install a D-number rule scoped to A, or use no matching rule for B.
5. Submit a B3-style `PlumbReq` with `ctx = ExecCtx::Window(B)` and text `D117573677`.
6. Assert that A's selection, visibility, and warp state do not change.
7. As a control, submit the same request from A and assert that the scoped rule may handle it.

The test should live near the existing plumbing integration coverage in `crates/apex-server/tests/socket.rs` or `crates/apex-server/tests/server.rs`.

## Conclusion

The bug is a cross-window fallback-search leak:

```text
terminal B3
  -> no window-scoped plumbing rule matches
  -> fallback Proposal::Look retains terminal ctx
  -> Proposal::Look nevertheless prefers global node.seltext
  -> node.seltext is Smartlog because Smartlog was opened
  -> Smartlog is searched, revealed, selected, and mouse-warped
```

The live state, strict rule checks, source control flow, and post-event selection all converge on this explanation.
