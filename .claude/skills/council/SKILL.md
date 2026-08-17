---
name: consulting-the-council
description: Use when a question is high-stakes enough that one model's answer should not be trusted on its own - cross-border tax and VAT treatment, legal structuring and contract risk, financial modelling assumptions, security-sensitive or hard-to-reverse architecture decisions. Runs the question past 4 frontier models from different labs in parallel and reports where they agree, where they contradict each other, and what none of them covered.
---

# Consulting the Council

`council` puts one question to 4 frontier models at once, has a judge model compare
their answers, and reports the comparison. Its value is not the merged answer -
it is knowing **where the models disagreed**, which is the part a single model
can never tell you.

## When to use it

Escalate to the council when a wrong answer is expensive and the question is
genuinely contested:

- Tax, VAT, and cross-border treatment where jurisdictions interact
- Legal structuring, entity choice, contract and liability risk
- Financial modelling where the assumptions drive the result
- Architecture decisions that are costly to reverse, and security trade-offs
- Any question where you notice yourself about to hedge

## When not to use it

- Anything you can verify by reading the code, the docs, or running a command
- Factual lookups with one correct answer
- Anything routine, low-stakes, or easily reversed
- Iterating on wording or style

A consultation costs real money and takes minutes. One call, on a question that
deserves it.

## Commands

```bash
council ask "<question>"            # human-readable
council ask "<question>" --json     # machine-readable; use this
council ask "<question>" --full     # include every model's full answer
council models                      # show the current council
```

Read a question from stdin when it is long or contains quotes:

```bash
council ask - <<'EOF'
<question>
EOF
```

## The current council

- **OpenAI** — `~openai/gpt-latest`
- **Anthropic** — `~anthropic/claude-opus-latest`
- **Google** — `~google/gemini-pro-latest`
- **xAI** — `~x-ai/grok-latest`

These are OpenRouter `~…-latest` aliases, so each one always resolves to that
lab's newest model in the family. The roster is rediscovered every 24 hours;
you do not need to update this file when a lab ships a new model.

## Reading the JSON

| Field | What to do with it |
|---|---|
| `synthesis` | The merged answer. Safe to quote as the council's position. |
| `analysis.contradictions` | **Where the panel split.** Never report these as settled. Surface them as open questions and say which model took which side. |
| `analysis.consensus` | Points every model agreed on. The strongest claims available. |
| `analysis.unique_insights` | Something only one model raised. Worth checking, but it is a minority view — attribute it. |
| `analysis.blind_spots` | What no model covered. Often the real risk. |
| `analysis.partial_coverage` | Raised by some but not all of the panel. |
| `responses[]` | Each model's full answer, for when the summary is not enough. |
| `sources[]` | Pages the panel actually retrieved. Cite these, not the models. |
| `failed[]` | Panellists that did not answer. **If non-empty the panel was degraded** — say so, and say how many of how many answered. |
| `usage.cost_usd` | What the run cost. |

### Rules

1. **Never flatten a disagreement.** If `contradictions` is non-empty, the
   question is open. Report both positions and who held them. Presenting a
   contested point as settled is the one failure mode that makes this tool
   worse than useless.
2. **Check `failed` before trusting the breadth.** A two-model answer from a
   four-model panel is not a four-model answer.
3. **Attribute unique insights.** One model saying something is weaker evidence
   than all of them saying it.
4. **Treat `blind_spots` as a to-do list**, not as trivia.
5. **This is not professional advice.** For legal, tax, and financial questions
   the output is input to a conversation with a qualified adviser, never a
   substitute for one.

## Cost and latency

A consultation runs 4 panel calls plus a judge and a synthesis pass, each with
web search enabled. Measured: **roughly 3 minutes and $0.35 per question** — far more than a
single completion.

- Do not call `council` in a loop.
- Do not call it to refine phrasing; get the question right first.
- One consultation per decision, not per message.

If you need to bound it, `--max-tool-calls 1` cuts the panel's web research to a
single step and is noticeably cheaper.

## Exit codes

`0` success (possibly degraded) · `2` usage error · `3` no API key ·
`4` API error · `5` every panellist failed · `6` timed out or cancelled
