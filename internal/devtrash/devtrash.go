// Package devtrash encuentra lo que generan las herramientas de desarrollo y se
// puede volver a crear. Solo lee: nunca borra.
package devtrash

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"el-buscador/internal/rules"
	"el-buscador/internal/scanner"
)

const (
	CategoryProject = "project"
	CategoryCache   = "cache"

	// Lo más pequeño solo estorba: no cambia cuánto espacio se recupera.
	minFindingSize = 1024 * 1024
	gitWorkers     = 8
)

// Finding es una carpeta detectada.
type Finding struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Project  string `json:"project"`
	Category string `json:"category"`
	Size     int64  `json:"size"`
	// LastActivity es la última modificación del proyecto sin contar lo generado,
	// en segundos Unix. Es 0 en los cachés.
	LastActivity int64        `json:"lastActivity"`
	Advice       rules.Advice `json:"advice"`
}

// Report es el resultado del análisis.
type Report struct {
	Findings []Finding `json:"findings"`
	// SafeSize suma solo lo marcado como seguro.
	SafeSize int64 `json:"safeSize"`
}

type lockfile struct {
	name    string
	command string
}

// projectRule describe una carpeta generada. Nunca basta el nombre: exige un
// marcador, porque "target" o "vendor" también pueden ser carpetas del usuario.
type projectRule struct {
	name string
	// markers son archivos en la carpeta padre; "*.ext" acepta cualquier nombre.
	markers []string
	// insideMarker es un archivo dentro de la propia carpeta.
	insideMarker string
	locks        []lockfile
	command      string
	what         rules.Text
}

var nodeLocks = []lockfile{
	{"pnpm-lock.yaml", "pnpm install"},
	{"bun.lock", "bun install"},
	{"bun.lockb", "bun install"},
	{"yarn.lock", "yarn install"},
	{"package-lock.json", "npm install"},
}

var projectRules = []projectRule{
	{
		name: "node_modules", markers: []string{"package.json"}, locks: nodeLocks, command: "npm install",
		what: rules.Text{ES: "Dependencias de Node.", EN: "Node dependencies."},
	},
	{
		name: "vendor", markers: []string{"composer.json"}, locks: []lockfile{{"composer.lock", "composer install"}}, command: "composer install",
		what: rules.Text{ES: "Dependencias de Composer.", EN: "Composer dependencies."},
	},
	{
		name: "vendor", markers: []string{"go.mod"}, locks: []lockfile{{"go.sum", "go mod vendor"}}, command: "go mod vendor",
		what: rules.Text{ES: "Dependencias de Go.", EN: "Go dependencies."},
	},
	{
		name: "target", markers: []string{"Cargo.toml"}, locks: []lockfile{{"Cargo.lock", "cargo build"}}, command: "cargo build",
		what: rules.Text{ES: "Compilación de Rust.", EN: "Rust build output."},
	},
	{
		name: "target", markers: []string{"pom.xml"}, command: "mvn package",
		what: rules.Text{ES: "Compilación de Maven.", EN: "Maven build output."},
	},
	{
		name: ".next", markers: []string{"package.json"}, command: "next build",
		what: rules.Text{ES: "Compilación de Next.js.", EN: "Next.js build output."},
	},
	{
		name: ".nuxt", markers: []string{"package.json"}, command: "nuxt build",
		what: rules.Text{ES: "Compilación de Nuxt.", EN: "Nuxt build output."},
	},
	{
		name: "dist", markers: []string{"package.json"}, command: "el comando de build del proyecto",
		what: rules.Text{ES: "Salida de compilación.", EN: "Build output."},
	},
	{
		name: "build", markers: []string{"package.json"}, command: "el comando de build del proyecto",
		what: rules.Text{ES: "Salida de compilación.", EN: "Build output."},
	},
	{
		name: "bin", markers: []string{"*.csproj"}, command: "dotnet build",
		what: rules.Text{ES: "Compilación de .NET.", EN: ".NET build output."},
	},
	{
		name: "obj", markers: []string{"*.csproj"}, command: "dotnet build",
		what: rules.Text{ES: "Compilación de .NET.", EN: ".NET build output."},
	},
	{
		name: ".venv", insideMarker: "pyvenv.cfg", locks: pythonLocks, command: "pip install",
		what: rules.Text{ES: "Entorno virtual de Python.", EN: "Python virtual environment."},
	},
	{
		name: "venv", insideMarker: "pyvenv.cfg", locks: pythonLocks, command: "pip install",
		what: rules.Text{ES: "Entorno virtual de Python.", EN: "Python virtual environment."},
	},
}

var pythonLocks = []lockfile{
	{"uv.lock", "uv sync"},
	{"poetry.lock", "poetry install"},
	{"Pipfile.lock", "pipenv install"},
	{"requirements.txt", "pip install -r requirements.txt"},
}

// skippedDirs tiene programas instalados y extensiones. Sus node_modules parecen
// de un proyecto, pero borrarlos rompe esas apps.
var skippedDirs = []string{"appdata", "windows", "program files", "program files (x86)", "programdata"}

func isSkippedDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") ||
		slices.Contains(skippedDirs, strings.ToLower(name))
}

func isGeneratedName(name string) bool {
	return slices.ContainsFunc(projectRules, func(rule projectRule) bool {
		return strings.EqualFold(rule.name, name)
	})
}

// Analyze busca basura dev en un escaneo terminado.
func Analyze(ctx context.Context, result *scanner.Result, catalog *rules.Catalog) (Report, error) {
	type candidate struct {
		entry scanner.Entry
		rule  projectRule
	}
	var candidates []candidate
	result.WalkDirs(func(entry scanner.Entry) bool {
		// Así se instalan las apps de Electron: es un programa, no un proyecto.
		if strings.HasSuffix(strings.ToLower(entry.Path), `\resources\app`) {
			return false
		}
		if rule, ok := matchRule(entry); ok {
			if entry.Size >= minFindingSize {
				candidates = append(candidates, candidate{entry, rule})
			}
			return false // lo de adentro ya está contado
		}
		return !isSkippedDir(entry.Name)
	})

	findings := make([]Finding, len(candidates))
	var wg sync.WaitGroup
	slots := make(chan struct{}, gitWorkers)
	for i, c := range candidates {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			project := filepath.Dir(c.entry.Path)
			findings[i] = Finding{
				Path:         c.entry.Path,
				Kind:         c.entry.Name,
				Project:      project,
				Category:     CategoryProject,
				Size:         c.entry.Size,
				LastActivity: result.LastActivity(project, isGeneratedName),
				Advice:       adviseProject(ctx, c.entry, c.rule, catalog),
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	findings = append(findings, findCaches(result, catalog)...)
	slices.SortFunc(findings, func(a, b Finding) int {
		switch {
		case a.Size > b.Size:
			return -1
		case a.Size < b.Size:
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})

	report := Report{Findings: findings}
	for _, finding := range findings {
		if finding.Advice.Level == rules.LevelSafe {
			report.SafeSize += finding.Size
		}
	}
	return report, nil
}

func matchRule(entry scanner.Entry) (projectRule, bool) {
	if entry.Unreadable {
		return projectRule{}, false
	}
	parent := filepath.Dir(entry.Path)
	for _, rule := range projectRules {
		if !strings.EqualFold(rule.name, entry.Name) {
			continue
		}
		if rule.insideMarker != "" && exists(filepath.Join(entry.Path, rule.insideMarker)) {
			return rule, true
		}
		for _, marker := range rule.markers {
			if hasMarker(parent, marker) {
				return rule, true
			}
		}
	}
	return projectRule{}, false
}

func hasMarker(dir, marker string) bool {
	suffix, isPattern := strings.CutPrefix(marker, "*")
	if !isPattern {
		return exists(filepath.Join(dir, marker))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(entry os.DirEntry) bool {
		return !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), suffix)
	})
}

// exists solo consulta atributos, así que no descarga archivos que están en la nube.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func adviseProject(ctx context.Context, entry scanner.Entry, rule projectRule, catalog *rules.Catalog) rules.Advice {
	project := filepath.Dir(entry.Path)
	lock, hasLock := findLock(project, rule.locks)
	command := rule.command
	if hasLock {
		command = lock.command
	}
	regenerate := rules.Text{ES: "Se vuelve a crear con: " + command, EN: "Recreate it with: " + commandEN(command)}

	var advice rules.Advice
	switch state := readGitState(ctx, project, entry.Name); {
	case state == gitTracked:
		advice = rules.Advice{
			Level:  rules.LevelKeep,
			Reason: rule.what.Join(rules.Text{ES: "Git la rastrea, así que es parte del proyecto.", EN: "Git tracks it, so it's part of the project."}),
		}
	case state == gitUnknown:
		advice = rules.Advice{
			Level:  rules.LevelReview,
			Reason: rule.what.Join(rules.Text{ES: "No pudimos comprobar con git si es parte del proyecto.", EN: "We couldn't check with git whether it's part of the project."}),
		}
	case state == gitIgnored:
		advice = rules.Advice{
			Level:  rules.LevelSafe,
			Reason: rule.what.Join(rules.Text{ES: "Git la ignora y se puede regenerar.", EN: "Git ignores it and it can be regenerated."}),
			Action: regenerate,
		}
	case hasLock:
		advice = rules.Advice{
			Level:  rules.LevelSafe,
			Reason: rule.what.Join(rules.Text{ES: "Su archivo de versiones (" + lock.name + ") permite regenerarla igual.", EN: "Its lockfile (" + lock.name + ") lets you regenerate it exactly."}),
			Action: regenerate,
		}
	default:
		advice = rules.Advice{
			Level:  rules.LevelReview,
			Reason: rule.what.Join(rules.Text{ES: "Git no la ignora y no encontramos con qué garantizar que quede igual.", EN: "Git doesn't ignore it and nothing guarantees it can be recreated the same."}),
			Action: regenerate,
		}
	}

	if hasLock && lock.name == "pnpm-lock.yaml" {
		advice.Reason = advice.Reason.Join(rules.Text{
			ES: "Usa pnpm: comparte archivos con su almacén, así que puede liberar menos de lo que marca.",
			EN: "It uses pnpm: it shares files with its store, so it may free less than shown.",
		})
	}
	if catalog.IsSynced(entry.Path) {
		if advice.Level == rules.LevelSafe {
			advice.Level = rules.LevelReview
		}
		advice.Reason = advice.Reason.Join(rules.SyncedWarning())
	}
	return advice
}

func commandEN(command string) string {
	if command == "el comando de build del proyecto" {
		return "the project's build command"
	}
	return command
}

func findLock(project string, locks []lockfile) (lockfile, bool) {
	for _, lock := range locks {
		if exists(filepath.Join(project, lock.name)) {
			return lock, true
		}
	}
	return lockfile{}, false
}

type cacheRule struct {
	base    string
	rel     string
	kind    string
	level   rules.Level
	what    rules.Text
	command string
}

func findCaches(result *scanner.Result, catalog *rules.Catalog) []Finding {
	env := catalog.Env()
	downloadsAgain := rules.Text{ES: "Se vuelve a descargar cuando hace falta.", EN: "It's downloaded again when needed."}
	caches := []cacheRule{
		{env.LocalAppData, `pnpm\store`, "pnpm", rules.LevelSafe, rules.Text{ES: "Almacén global de pnpm.", EN: "Global pnpm store."}, "pnpm store prune"},
		{env.LocalAppData, `npm-cache`, "npm", rules.LevelSafe, rules.Text{ES: "Caché de npm.", EN: "npm cache."}, "npm cache clean --force"},
		{env.LocalAppData, `Yarn\Cache`, "Yarn", rules.LevelSafe, rules.Text{ES: "Caché de Yarn.", EN: "Yarn cache."}, "yarn cache clean"},
		{env.LocalAppData, `Composer`, "Composer", rules.LevelSafe, rules.Text{ES: "Caché de Composer.", EN: "Composer cache."}, "composer clear-cache"},
		{env.LocalAppData, `go-build`, "Go build", rules.LevelSafe, rules.Text{ES: "Caché de compilación de Go.", EN: "Go build cache."}, "go clean -cache"},
		{env.Home, `go\pkg\mod`, "Go modules", rules.LevelSafe, rules.Text{ES: "Módulos descargados de Go.", EN: "Downloaded Go modules."}, "go clean -modcache"},
		{env.LocalAppData, `pip\Cache`, "pip", rules.LevelSafe, rules.Text{ES: "Caché de pip.", EN: "pip cache."}, "pip cache purge"},
		{env.Home, `.cargo\registry`, "Cargo", rules.LevelSafe, rules.Text{ES: "Paquetes descargados de Cargo.", EN: "Downloaded Cargo packages."}, ""},
		{env.Home, `.gradle\caches`, "Gradle", rules.LevelSafe, rules.Text{ES: "Caché de Gradle.", EN: "Gradle cache."}, ""},
		{env.Home, `.m2\repository`, "Maven", rules.LevelSafe, rules.Text{ES: "Paquetes descargados de Maven.", EN: "Downloaded Maven packages."}, ""},
	}

	var findings []Finding
	for _, cache := range caches {
		if cache.base == "" {
			continue
		}
		path := filepath.Join(cache.base, cache.rel)
		entry, ok := result.Lookup(path)
		if !ok || entry.Size < minFindingSize {
			continue
		}
		advice := rules.Advice{Level: cache.level, Reason: cache.what.Join(downloadsAgain)}
		if cache.command != "" {
			advice.Action = rules.Text{ES: "Límpialo con: " + cache.command, EN: "Clean it with: " + cache.command}
		}
		findings = append(findings, Finding{Path: entry.Path, Kind: cache.kind, Category: CategoryCache, Size: entry.Size, Advice: advice})
	}

	if env.LocalAppData != "" {
		if entry, ok := result.Lookup(filepath.Join(env.LocalAppData, "Docker")); ok && entry.Size >= minFindingSize {
			findings = append(findings, Finding{
				Path: entry.Path, Kind: "Docker", Category: CategoryCache, Size: entry.Size,
				Advice: rules.Advice{
					Level:  rules.LevelReview,
					Reason: rules.Text{ES: "Disco de Docker: imágenes, contenedores y volúmenes. Los volúmenes pueden tener datos que no se recuperan.", EN: "Docker's disk: images, containers and volumes. Volumes may hold data you can't recover."},
					Action: rules.Text{ES: "Limpia con docker system prune, que no toca volúmenes. El disco virtual puede no encogerse solo.", EN: "Clean with docker system prune, which leaves volumes alone. The virtual disk may not shrink on its own."},
				},
			})
		}
	}
	return findings
}
