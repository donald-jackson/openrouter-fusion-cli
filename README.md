# council

Ask one question to the current frontier model from OpenAI, Anthropic, Google and xAI
at the same time, have a judge model compare their answers, and see **where they
disagreed**.

A single model can tell you what it thinks. It cannot tell you what it glossed over, or
where an equally capable model would have said something else. That gap is the whole
reason this exists, so the output leads with the disagreements, not the summary.

Built on [OpenRouter's Fusion](https://openrouter.ai/docs/guides/features/server-tools/fusion)
server tool. Go, standard library only, no third-party dependencies.

```
$ council ask "Should a seed-stage startup store money as integer minor units or decimals?"

ANSWER
──────
  Use integer minor units (e.g. cents in a BIGINT) plus a currency code…

AGREED  (3)
───────────
  • Use integer minor units as the practical default…

RAISED BY ONE MODEL  (4)
────────────────────────
  ~anthropic/claude-opus-latest
      High-precision token amounts can overflow BIGINT/int64…

NOT COVERED BY ANYONE  (1)
──────────────────────────
  • None addressed rounding policy at settlement time…

────────────────────────────────────────────────────────────
4 of 4 answered · 50.9s · $0.1366 · 2570 tokens
```

## Install

### Homebrew (macOS and Linux)

```bash
brew install donald-jackson/tap/council
```

That taps `donald-jackson/homebrew-tap` and installs a prebuilt binary — Apple Silicon,
Intel, and Linux on both architectures. Upgrades come through `brew upgrade` like
anything else. Because Homebrew installs it, macOS does not quarantine it and no
`xattr` surgery is needed.

### From a release

```bash
gh release download v0.2.0 --repo donald-jackson/openrouter-fusion-cli \
  --pattern 'council_v0.2.0_darwin_arm64'
chmod +x council_v0.2.0_darwin_arm64
mv council_v0.2.0_darwin_arm64 /usr/local/bin/council
council version
```

`.tar.gz` archives for darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64 are
attached to the same release, with a `checksums.txt`. A binary downloaded through a
browser is quarantined by Gatekeeper because it is unsigned — clear it with
`xattr -d com.apple.quarantine ./council`.

### From source

```bash
go build -o bin/council ./cmd/council
```

### API key

```bash
council setup
```

It prompts for the key without echoing it, checks it against OpenRouter before saving
anything, and writes it to `~/.config/council/config.env` with mode `0600`. After that
`council` works from any directory. Get a key at
[openrouter.ai/keys](https://openrouter.ai/keys).

For scripts and CI, pipe it in instead — the prompt is skipped when stdin is not a
terminal:

```bash
echo "$OPENROUTER_API_KEY" | council setup --force
```

`council setup --show` reports which key is in effect and where it came from, without
changing anything.

Resolution order is `OPENROUTER_API_KEY` in the environment, then `.env` in the working
directory, then `~/.config/council/config.env` — first non-empty wins. Only that one
variable is read, and nothing is exported into your environment. Because the first two
win, `setup` tells you when it has just written a key that something closer will
override.

`council models` and `council skill` work without a key — the model catalog endpoint is
public. Only `ask` needs one.

## Use

```bash
council ask "<question>"                 # human-readable
council ask "<question>" --json          # machine-readable
council ask "<question>" --full          # include every model's complete answer
council ask - < question.txt             # read from stdin

council models                           # the current council, and cache age
council models --all                     # every ~latest alias OpenRouter publishes
council skill -o .claude/skills/council/SKILL.md

council setup                            # store the API key globally
council setup --show                     # which key is in use, and from where
```

Useful flags for `ask`: `--models` to override the panel, `--judge` / `--outer` to
choose who compares and who writes the final answer, `--max-tool-calls` to bound the
web research (1-16, minimum 1; 1 is much cheaper than the default 4), `--timeout`,
`--request-timeout` and `--retries` to tune per-attempt timeouts and retries of transient
failures such as `unexpected EOF`, `--raw` to dump the
unmodified API response, `--no-color`.

Exit codes: `0` success (possibly degraded) · `2` usage · `3` no API key ·
`4` API error · `5` every panellist failed · `6` timed out or cancelled.

## Cost

This is the part to read before wiring it into anything.

A consultation runs every panel member plus a judge and a synthesis pass, each with web
search enabled. Measured on this implementation:

| Configuration | Wall clock | Cost |
|---|---|---|
| 4 models, `--max-tool-calls 4`, research-heavy tax question | 6m41s | $1.26 |
| 4 models, `--max-tool-calls 1`, short technical question | 51s | $0.14 |
| 2 cheap models, `--max-tool-calls 1`, one-line question | 20s | $0.005 |

Dollars and minutes, not cents and seconds. Ask one good question rather than iterating.

## How the council is chosen

OpenRouter publishes `~author/family-latest` aliases — `~openai/gpt-latest`,
`~anthropic/claude-opus-latest`, `~google/gemini-pro-latest`, `~x-ai/grok-latest` and
seven more — each of which always resolves to the newest model in that family. Those
aliases are the source of truth. `council models` fetches the catalog (free, no auth),
seats one alias per lab, and caches the result for 24 hours.

The alternative — ranking OpenRouter's ~400 concrete models ourselves — was tried and
rejected during design. Ranking by recency picks whichever small fast model shipped most
recently (`gemini-3.7-flash`); ranking by price picks whatever expensive legacy model
never had a price cut (`o1-pro`). Neither is the flagship. The aliases already encode
the answer, and they stay correct without a release of this tool.

Within a lab, seats are chosen by an ordered family preference seeded from OpenRouter's
own Fusion Quality preset, falling back to the priciest remaining alias if a family
disappears. The preference matters: Anthropic prices `claude-fable-latest` above
`claude-opus-latest`, so a pure price rule would quietly swap that seat. Cheaper tiers
(`gpt-mini`) are excluded outright — an empty seat is more honest than a mini model
sitting on a frontier panel.

## Two things worth knowing about the API

Both were found by testing against the live API, and both are silent failures:

1. **`tool_choice` must force the fusion tool.** Left at the default `auto`, the outer
   model answers from its own knowledge and never convenes the panel. The request
   succeeds, returns in seconds, costs cents — and is an ordinary single-model answer
   wearing a council's clothes. `council` always names the tool explicitly.

2. **A low `--max-tokens` silently kills panellists.** Frontier panellists are reasoning
   models; a small cap is consumed by reasoning tokens before any prose is emitted, and
   the panellist is then reported as failed with *"Stream completed without producing
   any text"*. It reads like an upstream fault but is self-inflicted. `council` warns
   below 2000.

## Degraded panels

A panellist can fail while the rest answer. That is not an error — the judge analyses
whoever responded — but it does change what the answer is worth, so it is reported in
the footer (`3 of 4 answered`), in a dedicated section, and in `failed[]` in the JSON.
A two-model answer from a four-model panel is not a four-model answer.

## Licence

MIT. See [LICENSE](LICENSE).

## Tests

```bash
go test ./...                          # unit + golden tests, no network
go test ./internal/render -update      # regenerate golden files
```

The parser tests run against `internal/openrouter/testdata/fusion_response.json`, a real
captured API response, so a change in either the parser or the upstream shape shows up
as a failing test rather than a runtime surprise.
