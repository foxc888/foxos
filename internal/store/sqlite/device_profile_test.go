package sqlite

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

func TestDeviceSnapshotTracksTransitionsAndPreservesMetadata(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	first := time.Date(2026, 7, 27, 1, 2, 3, 0, time.UTC)
	observation := domain.DeviceObservation{MACAddress: "aa-bb-cc-dd-ee-ff", IPAddress: "10.0.0.20", HostName: "laptop", Interface: "bridge-lan", DHCPServer: "dhcp-lan", Online: true}
	if err := store.RecordDeviceSnapshot(ctx, []domain.DeviceObservation{observation}, first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateDeviceMetadata(ctx, observation.MACAddress, "Office laptop", "Framework", []string{"work", "trusted"}); err != nil {
		t.Fatal(err)
	}
	second := first.Add(5 * time.Minute)
	if err := store.RecordDeviceSnapshot(ctx, nil, second); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDeviceSnapshot(ctx, nil, second.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	profile, err := store.DeviceProfile(ctx, observation.MACAddress)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Online || profile.Alias != "Office laptop" || profile.Vendor != "Framework" || len(profile.Tags) != 2 {
		t.Fatalf("profile=%+v", profile)
	}
	if !profile.FirstSeen.Equal(first) || !profile.LastSeen.Equal(first) {
		t.Fatalf("first=%s last=%s", profile.FirstSeen, profile.LastSeen)
	}
	history, err := store.DevicePresenceHistory(ctx, observation.MACAddress, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Online || !history[1].Online {
		t.Fatalf("history=%+v", history)
	}
}

func TestDeviceMetadataUpdatesAreConcurrencySafe(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.RecordDeviceSnapshot(ctx, []domain.DeviceObservation{{MACAddress: "AA:BB:CC:DD:EE:FF", Online: true}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsFound := make(chan error, 8)
	for index := 0; index < 8; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.UpdateDeviceMetadata(ctx, "AA:BB:CC:DD:EE:FF", fmt.Sprintf("device-%d", index), "", []string{fmt.Sprintf("tag-%d", index)})
			if err != nil {
				errorsFound <- err
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	profile, err := store.DeviceProfile(ctx, "AA:BB:CC:DD:EE:FF")
	if err != nil || profile.Alias == "" || len(profile.Tags) != 1 {
		t.Fatalf("profile=%+v err=%v", profile, err)
	}
}
