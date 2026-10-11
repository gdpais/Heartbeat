package outsystems

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Timeout after 30000 ms", "Timeout after <num> ms"},
		{"Request 2f1c6a9e-0b7d-4c55-9a51-3c2b1d0e9f8a failed", "Request <guid> failed"},
		{"Request 2F1C6A9E-0B7D-4C55-9A51-3C2B1D0E9F8A failed", "Request <guid> failed"},
		{"User 'alice@example.com' not found", "User <str> not found"},
		{`Invalid value "12 Main St" for Address`, "Invalid value <str> for Address"},
		{"Object 0x7ffd3a2b at slot 4", "Object <hex> at slot <num>"},
		{"Hash 9f86d081884c7d65 mismatch", "Hash <hex> mismatch"},
		// Words made of hex letters stay words.
		{"Payment accepted by deadbeef gateway", "Payment accepted by deadbeef gateway"},
		{"  Line   1\n\tcolumn 2  ", "Line <num> column <num>"},
		{"Order #1234 for customer 'O''Brien' (id 77)", "Order #<num> for customer <str><str> (id <num>)"},
	}
	for _, tt := range tests {
		if got := normalize(tt.in); got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFingerprintGroupsErrorsThatDifferOnlyInValues(t *testing.T) {
	stack := "at Module.Action(Int32 id) in Order.cs:line 42\nat Module.Screen.Preparation()\nat Platform.Run()\nat Platform.Loop()"
	base := Fingerprint("Order 1234 not found for user 'alice'", stack)
	if len(base) != 16 {
		t.Fatalf("fingerprint %q is not 16 hex digits", base)
	}
	same := []struct{ message, stack string }{
		{"Order 98 not found for user 'bob'", stack},
		// Line numbers and frames after the third line do not matter.
		{"Order 1234 not found for user 'alice'", "at Module.Action(Int32 id) in Order.cs:line 57\nat Module.Screen.Preparation()\nat Platform.Run()\nat Other.Caller()"},
		// Nor do blank lines and indentation.
		{"Order 1234 not found for user 'alice'", "\n  at Module.Action(Int32 id) in Order.cs:line 42\n\n  at Module.Screen.Preparation()\n  at Platform.Run()"},
	}
	for _, tt := range same {
		if got := Fingerprint(tt.message, tt.stack); got != base {
			t.Errorf("Fingerprint(%q, …) = %s, want %s", tt.message, got, base)
		}
	}
	different := []struct{ message, stack string }{
		{"Customer 1234 not found for user 'alice'", stack},
		{"Order 1234 not found for user 'alice'", "at Module.OtherAction() in Order.cs:line 42\nat Module.Screen.Preparation()"},
		{"Order 1234 not found for user 'alice'", ""},
	}
	for _, tt := range different {
		if got := Fingerprint(tt.message, tt.stack); got == base {
			t.Errorf("Fingerprint(%q, %q) collides with the base error", tt.message, tt.stack)
		}
	}
}
