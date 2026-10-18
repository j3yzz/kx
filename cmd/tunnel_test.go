package cmd

import "testing"

func TestClientCommand(t *testing.T) {
	got := clientCommand("mysql -h {host} -P {port} && echo {port}", "127.0.0.1", 13306)
	if want := "mysql -h 127.0.0.1 -P 13306 && echo 13306"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
