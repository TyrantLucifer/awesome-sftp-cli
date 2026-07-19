package main

import "testing"

func TestObserveFinalRequiresVisiblePatternAndAbsentLoading(t *testing.T) {
	loaded := []byte("\x1b[1;1H> payload.txt\x1b[2;1HREAD-ONLY | loading\x1b[2;1HREAD-ONLY | sort:name        ")
	matched, err := observeFinal(loaded, 80, 4, []string{"> payload.txt"}, "READ-ONLY | loading")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("final loaded selection was not matched")
	}

	loading := []byte("\x1b[1;1H> payload.txt\x1b[2;1HREAD-ONLY | loading")
	matched, err = observeFinal(loading, 80, 4, []string{"> payload.txt"}, "READ-ONLY | loading")
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("loading selection matched as ready")
	}

	remoteLoaded := []byte("\x1b[1;1H> download.txt\x1b[2;1HREAD-ONLY | caps:3@1 | helper:L0 not_available | sort:name | hidden:off | cache:lru | normal")
	matched, err = observeFinal(remoteLoaded, 120, 4, []string{"> download.txt"}, "READ-ONLY | loading")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("final remote capability selection was not matched")
	}
}
