package client

import "testing"

// TestGetClmNextToken_ComputesFromRequestOffsetNotResponse is a regression test: the
// CLM API was never confirmed to echo back the offset it was asked for in its Offset
// response field. getClmNextToken used to trust that field directly, which — if the API
// ever returns Offset: 0 regardless of what was requested — would compute the same
// non-empty "next" token forever, looping the SDK's own pagination driver indefinitely
// across List()/Grants() calls (SearchFolders, ListGroups, ListMembers,
// ListPermissionSets, GetGroupMembers all rely on this shared helper, and none of them
// have their own non-advancing-token guard the way GetMemberGroups does for its
// internal loop). The fix: compute purely from what the caller actually requested
// (requestOffset) plus how many items came back — the response's own Offset field is
// no longer part of the signature at all, so this class of bug is now structurally
// impossible, not just guarded against.
func TestGetClmNextToken_ComputesFromRequestOffsetNotResponse(t *testing.T) {
	tests := []struct {
		name          string
		requestOffset int
		itemCount     int
		total         int
		wantEmpty     bool
		wantNextOfft  int
	}{
		{"advances to the next page", 0, 100, 250, false, 100},
		{"advances from a non-zero offset", 100, 100, 250, false, 200},
		{"terminates when the next offset reaches total", 200, 50, 250, true, 0},
		{"terminates when the next offset exceeds total", 200, 100, 250, true, 0},
		{"terminates on an empty page even if total says more remain", 100, 0, 250, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getClmNextToken(tt.requestOffset, tt.itemCount, tt.total)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("expected an empty (terminating) token, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("expected a non-empty continuation token, got empty")
			}
			decoded, err := decodeClmPageToken(got)
			if err != nil {
				t.Fatalf("decodeClmPageToken: %v", err)
			}
			if decoded.Offset != tt.wantNextOfft {
				t.Errorf("expected next offset %d, got %d", tt.wantNextOfft, decoded.Offset)
			}
		})
	}
}

// TestGetClmNextToken_IgnoresAnyEchoedOffsetShape documents that the function's
// signature makes it structurally impossible to accidentally read a response's own
// (possibly unreliable) Offset field: requestOffset is the only offset input, always
// supplied by the caller from what it actually asked for, never from a decoded
// response body.
func TestGetClmNextToken_IgnoresAnyEchoedOffsetShape(t *testing.T) {
	// Same requestOffset/itemCount/total as a real first page (offset=0, 100 items,
	// 250 total) must always produce the same next token, regardless of what a
	// hypothetical misbehaving API might have put in its own response Offset field —
	// there is no such field to read here at all.
	got := getClmNextToken(0, 100, 250)
	decoded, err := decodeClmPageToken(got)
	if err != nil {
		t.Fatalf("decodeClmPageToken: %v", err)
	}
	if decoded.Offset != 100 {
		t.Fatalf("expected next offset 100 computed purely from requestOffset+itemCount, got %d", decoded.Offset)
	}
}
