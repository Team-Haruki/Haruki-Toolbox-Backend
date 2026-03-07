package utils

import "testing"

func TestParseUploadDataType(t *testing.T) {
	t.Parallel()

	valid := []UploadDataType{
		UploadDataTypeSuite,
		UploadDataTypeMysekai,
		UploadDataTypeMysekaiBirthdayParty,
	}
	for _, want := range valid {
		got, err := ParseUploadDataType(string(want))
		if err != nil {
			t.Fatalf("ParseUploadDataType(%q) returned error: %v", want, err)
		}
		if got != want {
			t.Fatalf("ParseUploadDataType(%q) = %q, want %q", want, got, want)
		}
	}

	if _, err := ParseUploadDataType("unknown"); err == nil {
		t.Fatalf("ParseUploadDataType should fail on invalid data type")
	}
}

func TestParseSupportedDataUploadServer(t *testing.T) {
	t.Parallel()

	valid := []SupportedDataUploadServer{
		SupportedDataUploadServerJP,
		SupportedDataUploadServerEN,
		SupportedDataUploadServerTW,
		SupportedDataUploadServerKR,
		SupportedDataUploadServerCN,
	}
	for _, want := range valid {
		got, err := ParseSupportedDataUploadServer(string(want))
		if err != nil {
			t.Fatalf("ParseSupportedDataUploadServer(%q) returned error: %v", want, err)
		}
		if got != want {
			t.Fatalf("ParseSupportedDataUploadServer(%q) = %q, want %q", want, got, want)
		}
	}

	if _, err := ParseSupportedDataUploadServer("us"); err == nil {
		t.Fatalf("ParseSupportedDataUploadServer should fail on invalid server")
	}
}

func TestParseSupportedInheritUploadServer(t *testing.T) {
	t.Parallel()

	valid := []SupportedInheritUploadServer{
		SupportedInheritUploadServerJP,
		SupportedInheritUploadServerEN,
	}
	for _, want := range valid {
		got, err := ParseSupportedInheritUploadServer(string(want))
		if err != nil {
			t.Fatalf("ParseSupportedInheritUploadServer(%q) returned error: %v", want, err)
		}
		if got != want {
			t.Fatalf("ParseSupportedInheritUploadServer(%q) = %q, want %q", want, got, want)
		}
	}

	if _, err := ParseSupportedInheritUploadServer("tw"); err == nil {
		t.Fatalf("ParseSupportedInheritUploadServer should fail on invalid server")
	}
}
