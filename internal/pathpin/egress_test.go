package pathpin

import (
	"bytes"
	"testing"
)

func TestWarpAllowUserData(t *testing.T) {
	got := warpAllowUserData("eno1")
	if string(got) != warpAllowPrefix+"eno1" {
		t.Fatalf("userdata=%q", got)
	}
}

func TestOifNameDataPadsToIFNAMSIZ(t *testing.T) {
	got, err := oifNameData("eno1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != ifNameSize {
		t.Fatalf("len=%d", len(got))
	}
	if !bytes.Equal(got[:4], []byte("eno1")) || got[4] != 0 {
		t.Fatalf("data=%q", got)
	}
}

func TestOifNameDataRejectsEmptyAndLong(t *testing.T) {
	if _, err := oifNameData(""); err == nil {
		t.Fatal("empty name")
	}
	if _, err := oifNameData("abcdefghijklmno"); err != nil {
		t.Fatal(err)
	}
	if _, err := oifNameData("abcdefghijklmnop"); err == nil {
		t.Fatal("16-byte name")
	}
}
