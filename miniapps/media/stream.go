package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

func stream(ctx context.Context, update func(State) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := adapterCommand(ctx, "stream", "--no-diff", "--allow-missing-title")
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	readErr := readStates(stdout, update)
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("MediaRemote stream: %w", waitErr)
	}
	return fmt.Errorf("MediaRemote stream ended")
}

func readStates(stdout io.Reader, update func(State) error) error {
	decoder := json.NewDecoder(stdout)
	var previous State
	received := false
	for {
		var event struct {
			Type    string
			Payload State
		}
		// A missing position must be distinguishable from the start of a track.
		event.Payload.ElapsedTime = -1
		err := decoder.Decode(&event)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("decode MediaRemote stream: %w", err)
		}
		if event.Type != "data" || (received && event.Payload == previous) {
			continue
		}
		if err := update(event.Payload); err != nil {
			return err
		}
		previous, received = event.Payload, true
	}
}

func adapterCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	resources := filepath.Join(filepath.Dir(executable), "..", "Resources", "mediaremote")
	paths := []string{
		filepath.Join(resources, "mediaremote-adapter.pl"),
		filepath.Join(resources, "MediaRemoteAdapter.framework"),
	}
	return exec.CommandContext(ctx, "/usr/bin/perl", append(paths, args...)...), nil
}

func (c Control) commandID() int {
	// MediaRemote's send IDs for previous, toggle play/pause, and next.
	return [...]int{5, 2, 4}[c]
}

func sendControl(ctx context.Context, control Control) error {
	cmd, err := adapterCommand(ctx, "send", strconv.Itoa(control.commandID()))
	if err != nil {
		return err
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("MediaRemote send: %w: %s", err, output)
	}
	return nil
}
