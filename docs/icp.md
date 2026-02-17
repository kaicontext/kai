# Ideal Customer Profile (ICP)

Who we sell to in the next 6 months.

## One-Line ICP

30-120 engineer product teams running 2,000+ tests with 10+ minute CI times, using GitHub Actions or GitLab CI, and feeling pressure from increasing PR volume.

---

## Company Size

- 20-150 engineers total
- 5-40 engineers regularly touching backend code
- High enough PR volume that CI becomes a bottleneck

**Not a fit:**
- Teams under 10 engineers (CI pain not acute enough)
- Enterprises over 1,000 engineers (procurement cycles too long for design partner phase)

## Engineering Maturity

**Strong signals:**
- Monorepos or medium-to-large repos
- 2,000+ tests in the suite
- CI time over 8-10 minutes per PR
- High PR volume (10+ PRs/day)
- Already using GitHub Actions or GitLab CI
- Have attempted test splitting, caching, or parallelization

**Disqualifiers:**
- Very small test suites (under 500 tests)
- Teams that don't measure or care about CI time
- Heavy reliance on flaky integration tests with no intent to fix
- No automated test suite at all

## AI Adoption Signal

Strong ICP trait: teams where AI is increasing code output faster than CI can keep up.

Look for:
- Active Copilot / Cursor / AI coding tool usage
- Increased PR velocity over the past 6 months
- Complaints about CI scaling with AI-generated code
- Interest in AI-native development workflows

## Tech Stack

**Primary targets:**
- Go
- TypeScript / Node.js
- Python
- Ruby
- Rust

**Avoid for now:**
- Niche stacks with no tree-sitter grammar
- Mobile-only teams (iOS/Android build systems are a different problem)
- Teams locked into Bazel (already have graph-based build, different value prop)

## Buyer Persona

**Primary champion (the person who will evaluate and push for adoption):**
- Staff Engineer
- Dev Infra / Platform Engineer
- CI owner / Build Engineer
- Developer Experience lead

**Secondary sponsor (signs off on adoption):**
- VP Engineering
- Engineering Manager over platform team

**Not a fit as first contact:**
- Pure frontend teams
- Non-technical founders
- Procurement-first buyers

## Qualifying Questions

When prospecting, confirm pain with:

1. "How long does your CI take per PR?"
2. "How many PRs does your team open per day?"
3. "Have you tried test splitting, caching, or parallelization?"
4. "Is AI-generated code increasing your PR volume?"
5. "What percentage of CI time is spent running tests that aren't affected by the change?"

If they answer "CI takes 3 minutes and it's fine" — they are not ICP.

## Quick Filter Checklist

Use this to qualify a prospect in 10 seconds:

- [ ] 20-150 engineers
- [ ] 2,000+ tests
- [ ] CI time over 8 minutes
- [ ] GitHub Actions or GitLab CI
- [ ] Go, TypeScript, Python, Ruby, or Rust
- [ ] Technical champion available (not procurement-gated)
- [ ] Feeling CI pain (not just "it'd be nice")

5+ checks = strong prospect. Under 3 = move on.
