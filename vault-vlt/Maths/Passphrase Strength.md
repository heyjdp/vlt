---
title: Passphrase Strength
tags:
  - maths
  - security
created: 2026-10-27
---

# Passphrase Strength

All vault security reduces to the passphrase plus one scrypt derivation per guess. This note puts numbers on that.

## Entropy

For a passphrase chosen **uniformly at random** from a space of size $|\Omega|$:

$$
H = \log_2 |\Omega| \ \text{bits}
$$

| Scheme | $\lvert\Omega\rvert$ | $H$ |
|---|---|---|
| $L$ random printable ASCII chars | $94^{L}$ | $L \log_2 94 \approx 6.555\,L$ |
| $k$ Diceware words | $7776^{k}$ | $k \log_2 7776 \approx 12.925\,k$ |

> [!warning] Human-chosen passphrases
> These formulas only hold for random choice. Passphrases people make up have far less entropy than their length suggests, so treat them as much weaker.

## Brute-force cost

An attacker who can test $g$ guesses per second finds the passphrase in an expected

$$
\mathbb{E}[T] = \frac{2^{H-1}}{g}\ \text{seconds}
$$

Each guess costs one scrypt with $N = 2^{18}$, $r = 8$ (256 MiB, see [[Scrypt Cost Model]]). That memory cost is what keeps $g$ low. As an **illustrative, well-funded** attacker, assume $g = 10^{4}$ guesses/s across a GPU cluster.

| Passphrase | $H$ (bits) | $\mathbb{E}[T]$ at $g = 10^4/\text{s}$ |
|---|---:|---:|
| 8 random ASCII chars | 52.4 | $\approx 9.7\times10^{3}$ years |
| 4 Diceware words | 51.7 | $\approx 5.8\times10^{3}$ years |
| 5 Diceware words | 64.6 | $\approx 4.5\times10^{7}$ years |
| 6 Diceware words | 77.5 | $\approx 3.5\times10^{11}$ years |

Worked example for 5 words:

$$
\frac{2^{64.62-1}}{10^{4}} = \frac{1.42\times10^{19}}{10^{4}}\ \text{s} = 1.42\times10^{15}\ \text{s} \approx \frac{1.42\times10^{15}}{3.156\times10^{7}} \approx 4.5\times10^{7}\ \text{years}
$$

## Implications for vlt

- The create-vault screen recommends five or more random words and enforces a 12-character floor ([[F04 Confirm passphrase on vault creation]]).
- [[ADR-001 Scrypt-wrapped X25519 identity|ADR-001]] leaves these numbers unchanged. The attacker still needs one scrypt per guess against `vlt.age`.
- Raising the work factor to $\log_2 N = 19$ doubles both $\mathbb{E}[T]$ and the unlock time. With a single scrypt per unlock that becomes affordable, which it isn't today.

## Related
- [[Security Model]]
