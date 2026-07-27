# Meeting across networks without becoming the network

Everything worked on `127.0.0.1`. Two nodes in the same house found each
other and talked. But two nodes on *separate* home networks could do
neither of the two things they need to: they couldn't **find** each other
(rendezvous), and they couldn't **connect** (NAT — a home router lets you
dial out but never accepts an unsolicited dial in).

The first design decision was to split those two problems apart, because
they have different answers and conflating them produces mush. Rendezvous
is "how do I learn a peer exists and where to aim." NAT traversal is "the
socket won't open even though I know where to aim." We built them as
separate rungs: mDNS for the same-house case (free, no infrastructure),
a reachability check (our AutoNAT — a node asks a couple of peers to dial
it back, and believes it's publicly reachable only if one actually lands),
and then, for the hard case where *both* peers are NATed, a **relay**.

The second decision was the one with teeth, and it's a values decision as
much as a technical one. A relay is the natural place for a project to
plant infrastructure — "connect through our relay." We refused that. In
Silt a relay is a **node capability, not special infrastructure**: any
publicly reachable node can offer `-relay`, none is privileged, and *no
relay address is baked into the binary*. The public dev box we test
against lives in a separate `deploy/` directory, clearly labeled as
throwaway scaffolding the project does not operate. The network must not
depend on us to exist.

What makes that stance safe is that the relay is **content-blind by
construction**, not by promise. A relay splices two raw byte pipes; the
sender then runs its *normal* pinned end-to-end TLS handshake with the
target *through* the splice. So Silt's core security invariant — "a
frame's sender is whoever the TLS handshake authenticated" — holds
unchanged across a relay, because the relay only ever moves opaque bytes
it cannot read, alter, or forge. It learns metadata (which two node IDs
talked, when, how much), the same an on-path router already sees, and the
threat model says so plainly.

Then reality taught the lesson it always teaches. The first real
cross-network run — one Mac, one relay box, one field test — failed, and
the failure was invisible in every in-process simulation we'd ever run:
the transport dialed a *fresh connection per message*, so a reply to a
NATed peer required dialing *into* it, which is exactly the thing NAT
forbids. Bootstrap came back with zero peers and no error. The fix was to
make replies ride the connection the peer itself opened, and to stop
stamping undialable wildcard addresses into peers' address books. It was
a transport bug, in transport code — precisely the class the sim can't
see because the sim never opens a socket. That single field test is why
there is now a multi-process end-to-end suite that runs real daemons over
real TCP in CI: so the next bug of that shape dies in a pull request
instead of on a kitchen table across town.
