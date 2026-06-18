package main

import "testing"

// TestConfigureDefaultUsesList verifies that the bare `configure` subcommand
// enters the list-based TUI, not the direct-manipulation TUI.
func TestConfigureDefaultUsesList(t *testing.T) {
	origDirect, origList := configureDirect, configureList
	defer func() {
		configureDirect, configureList = origDirect, origList
	}()

	listCalled, directCalled := false, false
	configureList = func() bool {
		listCalled = true
		return false
	}
	configureDirect = func() {
		directCalled = true
	}

	runConfigure(nil)

	if !listCalled {
		t.Error("expected list TUI to be invoked for default configure")
	}
	if directCalled {
		t.Error("expected direct TUI NOT to be invoked for default configure")
	}
}

// TestConfigureDirectFlagUsesDirect verifies that `configure --direct` enters
// the direct-manipulation TUI and does not invoke the list UI first.
func TestConfigureDirectFlagUsesDirect(t *testing.T) {
	origDirect, origList := configureDirect, configureList
	defer func() {
		configureDirect, configureList = origDirect, origList
	}()

	listCalled, directCalled := false, false
	configureList = func() bool {
		listCalled = true
		return false
	}
	configureDirect = func() {
		directCalled = true
	}

	runConfigure([]string{"--direct"})

	if listCalled {
		t.Error("expected list TUI NOT to be invoked when --direct is passed")
	}
	if !directCalled {
		t.Error("expected direct TUI to be invoked when --direct is passed")
	}
}

// TestConfigureListSwitchToDirect verifies that when the list TUI signals it
// wants to switch to direct mode, the dispatcher launches the direct TUI.
func TestConfigureListSwitchToDirect(t *testing.T) {
	origDirect, origList := configureDirect, configureList
	defer func() {
		configureDirect, configureList = origDirect, origList
	}()

	listCalled, directCalled := false, false
	configureList = func() bool {
		listCalled = true
		return true // signal switch to direct
	}
	configureDirect = func() {
		directCalled = true
	}

	runConfigure(nil)

	if !listCalled {
		t.Error("expected list TUI to be invoked")
	}
	if !directCalled {
		t.Error("expected direct TUI to be invoked after list signals switch")
	}
}
