package rules

import "testing"

func testCatalog() *Catalog {
	return New(Env{
		SystemDrive:  "C:",
		Windir:       `C:\Windows`,
		ProgramData:  `C:\ProgramData`,
		ProgramFiles: []string{`C:\Program Files`, `C:\Program Files (x86)`},
		Home:         `C:\Users\ana`,
		LocalAppData: `C:\Users\ana\AppData\Local`,
		AppData:      `C:\Users\ana\AppData\Roaming`,
	})
}

func TestExplain(t *testing.T) {
	tests := []struct {
		path  string
		isDir bool
		want  Level
	}{
		{`C:\pagefile.sys`, false, LevelKeep},
		{`c:\PAGEFILE.SYS`, false, LevelKeep},
		{`C:\hiberfil.sys`, false, LevelReview},
		{`C:\Windows`, true, LevelKeep},
		{`C:\Windows\System32\kernel32.dll`, false, LevelKeep},
		{`C:\Windows\Temp\x.tmp`, false, LevelSafe},
		{`C:\Windows\SoftwareDistribution\Download`, true, LevelReview},
		{`C:\Program Files\App\app.exe`, false, LevelKeep},
		{`C:\Program Files (x86)\App`, true, LevelKeep},
		{`C:\Users\ana\AppData\Local\Temp\a.bin`, false, LevelSafe},
		{`C:\Users\ana\AppData\Local\App\data.iso`, false, LevelReview},
		{`C:\Users\ana\iCloudDrive\series`, true, LevelReview},
		{`C:\Users\ana\iCloudPhotos\Photos\a.jpg`, false, LevelReview},
		{`C:\Users\ana\OneDrive - Empresa\a.dmp`, false, LevelReview},
		{`C:\Users\ana\Downloads\windows.iso`, false, LevelReview},
		{`C:\Users\ana\Downloads\crash.DMP`, false, LevelSafe},
	}
	catalog := testCatalog()
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			advice, ok := catalog.Explain(test.path, test.isDir)
			if !ok {
				t.Fatal("sin regla")
			}
			if advice.Level != test.want {
				t.Errorf("Level = %s, quiero %s", advice.Level, test.want)
			}
		})
	}
}

// Lo desconocido no se clasifica: decir "seguro" por omisión borraría datos.
func TestExplainLeavesUnknownUnclassified(t *testing.T) {
	catalog := testCatalog()
	for _, path := range []string{
		`C:\Users\ana`,
		`C:\Users\ana\Documents\tesis.docx`,
		`C:\Media\pelicula.mkv`,
		`C:\pagefile.sys\hijo`,
		`C:\Users\ana\OneDriveNoEsNube.txt.bak\..`,
	} {
		if advice, ok := catalog.Explain(path, false); ok && path != `C:\pagefile.sys\hijo` {
			t.Errorf("%s: regla inesperada %+v", path, advice)
		}
	}
	if _, ok := catalog.Explain(`C:\pagefile.sys\hijo`, false); ok {
		t.Error("una regla sin herencia no aplica a sus hijos")
	}
}

func TestEveryAdviceHasBothLanguages(t *testing.T) {
	check := func(name string, advice Advice) {
		t.Helper()
		if advice.Reason.ES == "" || advice.Reason.EN == "" {
			t.Errorf("%s: falta motivo en algún idioma", name)
		}
		if (advice.Action.ES == "") != (advice.Action.EN == "") {
			t.Errorf("%s: acción solo en un idioma", name)
		}
	}
	for path, rule := range testCatalog().paths {
		check(path, rule.advice)
	}
	for ext, advice := range extensions {
		check(ext, advice)
	}
}

func TestEmptyEnvDoesNotCreateRules(t *testing.T) {
	catalog := New(Env{})
	if len(catalog.paths) != 0 {
		t.Errorf("reglas = %d, quiero 0", len(catalog.paths))
	}
	if catalog.IsSynced(`C:\Users\ana\iCloudDrive\a`) {
		t.Error("sin Home no se puede saber si está sincronizada")
	}
}
