package releasepack

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestThirdPartyNoticeBindsDeclaredAndResolvedModuleIdentity(t *testing.T) {
	modules := []Module{
		{
			Path: "example.com/original", Version: "v1.2.3", License: "BSD-2-Clause",
			Sum: "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", Targets: Targets[:],
			Replacement: &ModuleReplacement{
				Path: "example.com/fork", Version: "v1.2.4-0.20260718000000-abcdef123456",
				Sum: "h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			},
		},
		{Path: "example.com/plain", Version: "v2.0.0", License: "MIT", Sum: "h1:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=", Targets: Targets[:]},
	}
	materials := []ModuleNotice{
		noticeFixture("example.com/plain", "v2.0.0", "example.com/plain", "v2.0.0", "MIT", "plain license\n"),
		noticeFixture("example.com/original", "v1.2.3", "example.com/fork", "v1.2.4-0.20260718000000-abcdef123456", "BSD-2-Clause", "fork license\n"),
	}

	first, err := BuildThirdPartyNotice(modules, materials)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildThirdPartyNotice(modules, []ModuleNotice{materials[1], materials[0]})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("third-party notice depends on material order")
	}
	rendered := string(first)
	for _, contract := range []string{
		"AMSFTP THIRD-PARTY SOFTWARE NOTICES\n",
		"Declared module: example.com/original@v1.2.3\n",
		"Resolved module: example.com/fork@v1.2.4-0.20260718000000-abcdef123456\n",
		"SPDX license expression: BSD-2-Clause\n",
		"Declared module: example.com/plain@v2.0.0\n",
		"fork license\n",
		"plain license\n",
	} {
		if strings.Count(rendered, contract) != 1 {
			t.Fatalf("notice contract %q count != 1:\n%s", contract, rendered)
		}
	}
	if strings.Count(rendered, "License source: LICENSE\n") != 2 {
		t.Fatalf("notice must retain one LICENSE source per module:\n%s", rendered)
	}
}

func TestThirdPartyNoticeRejectsIncompleteOrDriftedMaterialInventory(t *testing.T) {
	modules := []Module{
		{Path: "example.com/one", Version: "v1.0.0", License: "MIT", Sum: "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", Targets: Targets[:]},
		{Path: "example.com/two", Version: "v2.0.0", License: "BSD-3-Clause", Sum: "h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=", Targets: Targets[:]},
	}
	one := noticeFixture("example.com/one", "v1.0.0", "example.com/one", "v1.0.0", "MIT", "one\n")
	two := noticeFixture("example.com/two", "v2.0.0", "example.com/two", "v2.0.0", "BSD-3-Clause", "two\n")

	for name, mutate := range map[string]func([]ModuleNotice) []ModuleNotice{
		"missing module": func(values []ModuleNotice) []ModuleNotice { return values[:1] },
		"extra module": func(values []ModuleNotice) []ModuleNotice {
			return append(values, noticeFixture("example.com/extra", "v1.0.0", "example.com/extra", "v1.0.0", "MIT", "extra\n"))
		},
		"duplicate module": func(values []ModuleNotice) []ModuleNotice { return append(values, values[0]) },
		"declared version drift": func(values []ModuleNotice) []ModuleNotice {
			values[0].Version = "v1.0.1"
			return values
		},
		"resolved identity drift": func(values []ModuleNotice) []ModuleNotice {
			values[0].ResolvedPath = "example.com/not-one"
			return values
		},
		"license drift": func(values []ModuleNotice) []ModuleNotice {
			values[0].License = "Apache-2.0"
			return values
		},
		"file hash drift": func(values []ModuleNotice) []ModuleNotice {
			values[0].Files[0].SHA256 = strings.Repeat("0", 64)
			return values
		},
		"file bytes drift": func(values []ModuleNotice) []ModuleNotice {
			values[0].Files[0].Bytes = []byte("changed\n")
			return values
		},
		"missing final newline": func(values []ModuleNotice) []ModuleNotice {
			values[0].Files[0].Bytes = []byte("one")
			values[0].Files[0].SHA256 = noticeDigest(values[0].Files[0].Bytes)
			return values
		},
		"unsafe file name": func(values []ModuleNotice) []ModuleNotice {
			values[0].Files[0].Name = "../LICENSE"
			return values
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneModuleNotices([]ModuleNotice{one, two})
			if output, err := BuildThirdPartyNotice(modules, mutate(candidate)); err == nil {
				t.Fatalf("invalid notice inventory accepted: %q", output)
			}
		})
	}
}

func TestThirdPartyNoticePreservesMultipleReviewedLicenseFiles(t *testing.T) {
	modules := []Module{{Path: "modernc.org/example", Version: "v1.0.0", License: "BSD-3-Clause AND MIT", Sum: "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", Targets: Targets[:]}}
	material := noticeFixture("modernc.org/example", "v1.0.0", "modernc.org/example", "v1.0.0", "BSD-3-Clause AND MIT", "root license\n")
	thirdParty := []byte("third-party terms\n")
	material.Files = append(material.Files, NoticeFile{Name: "LICENSE-3RD-PARTY.md", SHA256: noticeDigest(thirdParty), Bytes: thirdParty})

	output, err := BuildThirdPartyNotice(modules, []ModuleNotice{material})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(output)
	if strings.Count(rendered, "License source: LICENSE\n") != 1 || strings.Count(rendered, "License source: LICENSE-3RD-PARTY.md\n") != 1 || !strings.Contains(rendered, "third-party terms\n") {
		t.Fatalf("multiple reviewed files were not preserved:\n%s", rendered)
	}
}

func noticeFixture(path, version, resolvedPath, resolvedVersion, license, body string) ModuleNotice {
	raw := []byte(body)
	return ModuleNotice{
		Path: path, Version: version, ResolvedPath: resolvedPath, ResolvedVersion: resolvedVersion, License: license,
		Files: []NoticeFile{{Name: "LICENSE", SHA256: noticeDigest(raw), Bytes: raw}},
	}
}

func noticeDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func cloneModuleNotices(values []ModuleNotice) []ModuleNotice {
	cloned := make([]ModuleNotice, len(values))
	for index, value := range values {
		cloned[index] = value
		cloned[index].Files = append([]NoticeFile(nil), value.Files...)
		for fileIndex := range cloned[index].Files {
			cloned[index].Files[fileIndex].Bytes = append([]byte(nil), value.Files[fileIndex].Bytes...)
		}
	}
	return cloned
}
