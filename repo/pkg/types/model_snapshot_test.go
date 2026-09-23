package types

import "testing"

func TestModelSnapshotValidateAcceptsZeroLengthFilesAndBindsObjectKeys(t *testing.T) {
	s := ModelSnapshot{Schema: ModelSnapshotSchema, Revision: "abc", TotalSizeBytes: 2, Files: []ModelSnapshotFile{
		{Path: "config.json", SizeBytes: 0, SHA256: "0000000000000000000000000000000000000000000000000000000000000000", ObjectKey: "model/import-1/snapshot/files/config.json"},
		{Path: "weights/a.bin", SizeBytes: 2, SHA256: "1111111111111111111111111111111111111111111111111111111111111111", ObjectKey: "model/import-1/snapshot/files/weights/a.bin"},
	}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestModelSnapshotValidateRejectsTraversalDuplicateAndTotalMismatch(t *testing.T) {
	base := ModelSnapshot{Schema: ModelSnapshotSchema, Revision: "abc", TotalSizeBytes: 0, Files: []ModelSnapshotFile{{Path: "x", SHA256: "0000000000000000000000000000000000000000000000000000000000000000", ObjectKey: "model/import-1/snapshot/files/x"}}}
	for name, mutate := range map[string]func(*ModelSnapshot){
		"traversal": func(s *ModelSnapshot) {
			s.Files[0].Path = "../x"
			s.Files[0].ObjectKey = "model/import-1/snapshot/files/../x"
		},
		"dot": func(s *ModelSnapshot) {
			s.Files[0].Path = "."
			s.Files[0].ObjectKey = "model/import-1/snapshot/files/."
		},
		"duplicate": func(s *ModelSnapshot) { s.Files = append(s.Files, s.Files[0]) },
		"total":     func(s *ModelSnapshot) { s.TotalSizeBytes = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			s := base
			mutate(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
}
