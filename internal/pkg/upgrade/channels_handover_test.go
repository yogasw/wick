package upgrade

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The draining side's offer travels in its drain record, and only a
// successor (another live pid) reads it as one.
func TestChannelsOfferInDrainRecord(t *testing.T) {
	t.Cleanup(func() { channelsReleased.Store(false) })
	other := os.Getppid() // alive, and not this process

	dir := t.TempDir()
	PublishDrainState(dir, other, time.Now(), nil)
	if PredecessorReleasedChannels(dir) {
		t.Fatal("offer reported before ReleaseChannels")
	}

	ReleaseChannels()
	if !ChannelsReleased() {
		t.Fatal("ChannelsReleased false after ReleaseChannels")
	}
	PublishDrainState(dir, other, time.Now(), nil)
	if !PredecessorReleasedChannels(dir) {
		t.Fatal("successor did not see the offer")
	}

	// The draining process never reads its own offer as a predecessor's.
	PublishDrainState(dir, os.Getpid(), time.Now(), nil)
	if PredecessorReleasedChannels(dir) {
		t.Fatal("own record read as a predecessor's offer")
	}

	// A record from a binary that predates the offer carries none.
	old := `{"pid":` + strconv.Itoa(other) + `,"since":"` + time.Now().Format(time.RFC3339) +
		`","outstanding":["agent turns=1"],"updated_at":"` + time.Now().Format(time.RFC3339Nano) + `"}`
	if err := os.WriteFile(drainStatePath(dir), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if PredecessorReleasedChannels(dir) {
		t.Fatal("an older record was read as an offer")
	}
}

// The successor's answer counts only when it names another live process.
func TestChannelsTakenByOther(t *testing.T) {
	dir := t.TempDir()
	if ChannelsTakenByOther(dir) {
		t.Fatal("no record, yet taken")
	}
	MarkChannelsTaken(dir) // written by this process: an earlier handover's
	if ChannelsTakenByOther(dir) {
		t.Fatal("own record read as the successor's")
	}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, channelsTakenFile), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(strconv.Itoa(os.Getppid()) + "\n")
	if !ChannelsTakenByOther(dir) {
		t.Fatal("a live successor's record was not seen")
	}
	write("0\n")
	if ChannelsTakenByOther(dir) {
		t.Fatal("a dead pid counted")
	}
	write("garbage")
	if ChannelsTakenByOther(dir) {
		t.Fatal("garbage counted")
	}
}
