# Convergent Encryption: deduplication vs. secrecy

## The tension

A content-addressed store deduplicates for free: identical bytes hash to
the same ID, so the network stores them once no matter how many people
add them. Encryption normally destroys this: good encryption is
*randomized*, so your encryption of `ubuntu-24.04.iso` and mine share not
a single byte, and the network stores every copy separately.

Convergent encryption is the classic trick for having (most of) both.

## The trick

Derive the key *from the plaintext itself*:

```
secret = SHA-256(plaintext chunk)          ← anyone with the chunk gets the same secret
key    = HKDF(secret, "key")
nonce  = HKDF(secret, "nonce")
ct     = AES-256-GCM(key, nonce, plaintext)
```

Same plaintext ⇒ same key ⇒ same ciphertext ⇒ same chunk ID ⇒ dedup
works across *everyone*, even though the stored bytes are encrypted and
useless to anyone who doesn't already know the plaintext... or the
secret, which silt records in the file's manifest. Holding a manifest
= able to decrypt; holding only chunks = holding noise.

## Wait — a fixed nonce with GCM?

Reusing a (key, nonce) pair for two different messages is catastrophic in
GCM (it leaks the XOR of plaintexts and can leak the authentication key).
Here it's safe *by construction*: the key is a function of the exact
plaintext, so the only way to reuse the pair is to encrypt the same
message again — which produces the same ciphertext, revealing nothing
new. Determinism isn't a bug here; it is the entire feature.

## The confirmation attack (why this isn't for secrets)

Deterministic encryption has an unavoidable leak: **anyone can test a
guess.** An attacker who suspects you stored a specific document runs the
same public recipe on their guess and checks whether the resulting chunk
ID exists in the network. Chunk present ⇒ guess confirmed.

The damage scales with guessability:

- *Public data* (OS images, media, datasets): nothing to confirm that
  isn't already public. Convergent is ideal — this is why it's the
  default for `silt add`.
- *Low-entropy private data* (a form letter with your salary in one
  blank): an attacker can enumerate every plausible fill-in and confirm
  which one you stored. A few thousand SHA-256 calls; disastrous.
- *High-entropy secrets*: unguessable, so unconfirmable — but if your
  data is unguessable anyway, you lose nothing by using `private` mode.

Rule of thumb: convergent encryption protects data exactly as well as
that data is *unguessable*. For anything personal, `silt add
-mode private` uses a random per-file key (index-bound nonces, no dedup,
no confirmation surface).

Real-world note: this attack is not hypothetical — it's why Dropbox-era
"cross-user deduplication" designs were abandoned, and the literature
(Douceur et al. 2002, who named convergent encryption; the "DupLESS"
line of work) revolves around blunting exactly this leak.

Code: `core/crypto/crypto.go`. Tests prove determinism (dedup),
tamper-rejection, and that private mode binds each chunk to its index so
ciphertexts can't be reordered.
