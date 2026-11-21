package main

import "testing"

func TestParseLaunchArgs(t *testing.T) {
	got, err := parseLaunchArgs(`["--lang=id-ID","--enable-features=SomethingNew","--force-dark-mode"]`)
	if err != nil || len(got) != 3 {
		t.Fatalf("allowed flags: got %v, %v", got, err)
	}
	if got, err := parseLaunchArgs(""); err != nil || got != nil {
		t.Fatalf("empty: got %v, %v", got, err)
	}
	for _, bad := range []string{
		`["--remote-debugging-port=9222"]`,
		`["--user-data-dir=/x"]`,
		`["--proxy-server=http://p:1"]`,
		`["--ignore-certificate-errors"]`,
		`["--load-extension=/x"]`,
		`["--disk-cache-dir=/x"]`,
		`["--renderer-cmd-prefix=sh"]`,
		`["--Headless=new"]`,
		`["--no-sandbox"]`,
		`["lang=id"]`,
		`["--lang","id"]`,
		`not json`,
	} {
		if _, err := parseLaunchArgs(bad); err == nil {
			t.Errorf("%s: want error", bad)
		}
	}
}
