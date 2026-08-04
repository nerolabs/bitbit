package node

import (
	"testing"

	"github.com/nerolabs/silt/core/por"
)

// Blind red-team F4 (integrity, S1) INVERTED as a regression.
//
// The candidate break: the auditor trusted the prover's self-reported block
// count on the LAST leaf. A liar holding only block 0 of a 40-block shard could
// report PorBlocks=1; the old tail-leniency branch accepted any 1..wantFull, and
// porChallenge clamped the sample space to block 0 alone, so the liar proved
// over the one block it kept and passed — collecting rent while holding ~1/40th
// of the shard. (Every stored shard is in fact full-size — chunk.Split pads the
// tail — so the leniency was unwarranted.)
//
// The fix: the auditor grades EVERY leaf against the same AUTHORITATIVE full
// block count it derives itself (wantFull), never the prover's self-report. This
// test reconstructs the exact gradeAnswers predicate over the real crypto and
// asserts the shrink liar now FAILS, while an honest holder still PASSES.
func TestTailShardShrinkIsRejected(t *testing.T) {
	k := DerivePorKey(linkLayoutKey(t))
	unit := []byte("tail-shard-chunk-id")

	// One block = SectorsPerBlock*SectorBytes bytes; build an honest 40-block shard.
	blockBytes := por.DefaultParams.SectorsPerBlock * por.SectorBytes
	const nBlocks = 40
	data := make([]byte, blockBytes*nBlocks)
	for i := range data {
		data[i] = byte(i*131 + 7)
	}
	tags := k.Tags(unit, data)
	realBlocks := len(tags)
	if realBlocks != nBlocks {
		t.Fatalf("setup: expected %d blocks, got %d", nBlocks, realBlocks)
	}

	// The auditor's AUTHORITATIVE count for this shard — wantFull, which it
	// derives itself for every leaf. The prover does not get to choose it.
	want := realBlocks
	seed := porChallengeSeed(12345)

	// ---- Shrink liar: keep only block 0 + tag 0, report PorBlocks=1 ----
	// It proves over a challenge clamped to its self-report (block 0 only), the
	// strongest form of the attack.
	liarData := data[:blockBytes]
	liarTags := tags[:1]
	reportedBlocks := 1
	cShrunk := porChallenge(seed, reportedBlocks, porSampleCount)
	prLiar, err := por.Prove(por.DefaultParams, liarData, liarTags, cShrunk)
	if err != nil {
		t.Fatalf("liar prove: %v", err)
	}

	// gradeAnswers grades over the AUTHORITATIVE want, not the self-report:
	//   passed := a.valid && blocksOK(a.blocks, want) &&
	//             porKey.Verify(id, porChallenge(seed, want, count), proof)
	liarPassed := true && // a.valid: the Merkle proof over the chunk id still binds
		blocksOK(reportedBlocks, want) &&
		k.Verify(unit, porChallenge(seed, want, porSampleCount), prLiar)
	if liarPassed {
		t.Fatalf("F4 regression: a liar holding 1 of %d blocks was graded PASSED — "+
			"the auditor must demand the committed block count, not the self-report", realBlocks)
	}

	// The count check alone must reject it: reported 1 != authoritative want.
	if blocksOK(reportedBlocks, want) {
		t.Fatalf("F4 regression: blocksOK accepted reported=%d against want=%d", reportedBlocks, want)
	}

	// Even a liar that LIES about its count (reports want) cannot pass: it must
	// then prove over the full sample space, which samples blocks it dropped.
	cFull := porChallenge(seed, want, porSampleCount)
	paddedTags := make([][]byte, want)
	copy(paddedTags, tags) // it kept the tags (receipts)
	liarFullData := make([]byte, len(data))
	copy(liarFullData[:blockBytes], liarData) // only block 0's bytes are real
	prPad, err := por.Prove(por.DefaultParams, liarFullData, paddedTags, cFull)
	if err != nil {
		t.Fatalf("padded liar prove: %v", err)
	}
	if k.Verify(unit, cFull, prPad) {
		t.Fatal("F4 regression: a liar reporting the true count but missing the bytes verified — crypto must reject it")
	}

	// CONTRAST: an honest holder (all bytes, reports the true count) PASSES.
	prHonest, err := por.Prove(por.DefaultParams, data, tags, cFull)
	if err != nil {
		t.Fatalf("honest prove: %v", err)
	}
	honestPassed := blocksOK(want, want) && k.Verify(unit, cFull, prHonest)
	if !honestPassed {
		t.Fatal("an honest holder of the full shard must pass the audit")
	}
}
