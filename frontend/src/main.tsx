import { createContext, render } from "preact";
import { useContext, useEffect, useMemo, useState } from "preact/hooks";
import { CleanupService, DiskService, ScanService } from "../bindings/el-buscador";
import { CleanupDialog, focusRing } from "./cleanup";
import { dictionary, I18n, initialLang, makeFormat, saveLang, useI18n, type Lang } from "./i18n";
import "./style.css";

type Usage = Awaited<ReturnType<typeof DiskService.Usage>>;
type Status = Awaited<ReturnType<typeof ScanService.Status>>;
type Summary = Awaited<ReturnType<typeof ScanService.Summary>>;
type Folder = Awaited<ReturnType<typeof ScanService.Listing>>;
type Entry = NonNullable<Summary["topFiles"]>[number];
type Finding = NonNullable<Summary["dev"]["findings"]>[number];
type Advice = Finding["advice"];
type AdviceMap = Summary["advice"];
type Tab = "files" | "dirs" | "dev" | "explore";

const POLL_MS = 250;
const GB = 1024 ** 3;

// Selection es lo marcado para limpiar. Se comparte entre todas las vistas de
// resultados, y removed oculta lo que ya se fue a la papelera.
const Selection = createContext({
  selected: new Map<string, number>(),
  removed: new Set<string>(),
  toggle: (_path: string, _size: number) => {},
});

function failedStatus(err: unknown): Status {
  return { state: "failed", error: String(err), progress: { files: 0, dirs: 0, size: 0, current: "" } };
}

function App() {
  const [lang, setLang] = useState<Lang>(initialLang);
  const [usage, setUsage] = useState<Usage | null>(null);
  const [usageFailed, setUsageFailed] = useState(false);
  const [status, setStatus] = useState<Status | null>(null);
  const [summary, setSummary] = useState<Summary | null>(null);

  const i18n = useMemo(
    () => ({
      lang,
      t: dictionary(lang),
      format: makeFormat(lang),
      toggle() {
        const next = lang === "es" ? "en" : "es";
        saveLang(next);
        setLang(next);
      },
    }),
    [lang],
  );

  useEffect(() => {
    document.documentElement.lang = lang;
  }, [lang]);

  useEffect(() => {
    DiskService.Usage().then(setUsage).catch((err) => {
      console.error(err);
      setUsageFailed(true);
    });
  }, [summary]);

  const running = status?.state === "scanning" || status?.state === "analyzing";
  useEffect(() => {
    if (!running) return;
    const timer = setInterval(async () => {
      try {
        const next = await ScanService.Status();
        if (next.state === "done") setSummary(await ScanService.Summary());
        setStatus(next);
      } catch (err) {
        console.error(err);
        setStatus(failedStatus(err));
      }
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [running]);

  async function start() {
    setSummary(null);
    try {
      await ScanService.Start();
      const next = await ScanService.Status();
      // Un escaneo muy corto ya pudo terminar: no quedaría nada que sondear.
      if (next.state === "done") setSummary(await ScanService.Summary());
      setStatus(next);
    } catch (err) {
      console.error(err);
      setStatus(failedStatus(err));
    }
  }

  return (
    <I18n.Provider value={i18n}>
      {summary ? (
        <Results summary={summary} usage={usage} onRescan={start} />
      ) : running && status ? (
        <Scanning status={status} />
      ) : (
        <Home usage={usage} usageFailed={usageFailed} status={status} onStart={start} />
      )}
    </I18n.Provider>
  );
}

function LangToggle() {
  const { t, toggle } = useI18n();
  return (
    <button
      type="button"
      onClick={toggle}
      aria-label={t.switchLang}
      title={t.switchLang}
      class={`cursor-pointer rounded-lg border border-gray-700 px-3 py-2 text-sm text-gray-300 hover:bg-gray-800 ${focusRing}`}
    >
      {t.switchLangShort}
    </button>
  );
}

function Home(props: { usage: Usage | null; usageFailed: boolean; status: Status | null; onStart: () => void }) {
  const { usage, usageFailed, status, onStart } = props;
  const { t, format } = useI18n();
  const usedPercent = usage ? ((usage.total - usage.free) / usage.total) * 100 : 0;

  return (
    <main class="relative flex h-full flex-col items-center justify-center gap-8 px-6 text-center">
      <div class="absolute top-4 right-4">
        <LangToggle />
      </div>

      <svg viewBox="0 0 100 100" width="64" height="64" class="text-emerald-400" aria-label="El Buscador" role="img">
        <rect x="14" y="14" width="44" height="72" rx="7" fill="currentColor" />
        <rect x="64" y="14" width="22" height="42" rx="6" fill="currentColor" opacity=".7" />
        <rect x="64" y="62" width="22" height="24" rx="6" fill="currentColor" opacity=".4" />
      </svg>

      <section class="w-full max-w-md" aria-live="polite">
        {usageFailed && <h1 class="text-2xl font-semibold">{t.diskReadFailed}</h1>}
        {!usageFailed && !usage && <h1 class="text-2xl font-semibold text-gray-400">{t.checkingDisk}</h1>}
        {usage && (
          <>
            <h1 class="text-2xl font-semibold">{t.diskFree(usage.drive, format.size(usage.free), format.size(usage.total))}</h1>
            <Bar percent={usedPercent} label={t.spaceUsed} />
          </>
        )}
      </section>

      <button
        type="button"
        onClick={onStart}
        class={`cursor-pointer rounded-xl bg-emerald-400 px-8 py-4 text-lg font-semibold text-gray-950 hover:bg-emerald-300 ${focusRing}`}
      >
        {t.scanButton}
      </button>

      <div class="text-sm text-gray-400" role="status">
        {status?.state === "canceled" && <p class="mb-2">{t.canceledNote}</p>}
        {status?.state === "failed" && <p class="mb-2 text-red-300">{t.failedNote}</p>}
        <p>{t.promise}</p>
      </div>
    </main>
  );
}

function Scanning({ status }: { status: Status }) {
  const { t, format } = useI18n();
  const { files, size, current } = status.progress;
  const analyzing = status.state === "analyzing";
  return (
    <main class="flex h-full flex-col items-center justify-center gap-6 px-6 text-center">
      <div role="status">
        <h1 class="text-2xl font-semibold">{analyzing ? t.checkingProjects : t.checkingDisk}</h1>
        <p class="mt-2 text-gray-400">{t.progress(format.number(files), format.size(size))}</p>
      </div>
      <p class="w-full max-w-2xl truncate text-sm text-gray-500" dir="ltr" title={current}>
        {analyzing ? " " : current}
      </p>
      <button
        type="button"
        onClick={() => ScanService.Cancel().catch(console.error)}
        class={`cursor-pointer rounded-lg border border-gray-600 px-5 py-2 text-gray-200 hover:bg-gray-800 ${focusRing}`}
      >
        {t.cancel}
      </button>
    </main>
  );
}

function Results(props: { summary: Summary; usage: Usage | null; onRescan: () => void }) {
  const { summary, usage, onRescan } = props;
  const { t, format } = useI18n();
  const [tab, setTab] = useState<Tab>("files");
  const [selected, setSelected] = useState(new Map<string, number>());
  const [removed, setRemoved] = useState(new Set<string>());
  const [cleaning, setCleaning] = useState<string[] | null>(null);

  const selection = useMemo(
    () => ({
      selected,
      removed,
      toggle(path: string, size: number) {
        setSelected((current) => {
          const next = new Map(current);
          if (!next.delete(path)) next.set(path, size);
          return next;
        });
      },
    }),
    [selected, removed],
  );
  const selectedSize = Array.from(selected.values()).reduce((total, size) => total + size, 0);

  function onCleaned(paths: string[]) {
    setRemoved((current) => new Set(Array.from(current).concat(paths)));
    setSelected((current) => {
      const next = new Map(current);
      for (const path of paths) next.delete(path);
      return next;
    });
  }

  const findings = summary.dev.findings ?? [];
  const used = usage ? usage.total - usage.free : 0;
  const uncounted = used - summary.size;
  const tabs: { id: Tab; label: string }[] = [
    { id: "files", label: t.tabFiles },
    { id: "dirs", label: t.tabFolders },
    ...(findings.length > 0 ? [{ id: "dev" as const, label: t.tabDev }] : []),
    { id: "explore", label: t.tabExplore },
  ];

  return (
    <Selection.Provider value={selection}>
    <main class="mx-auto flex h-full max-w-5xl flex-col gap-4 px-6 py-5" inert={cleaning !== null}>
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold">{t.found(format.size(summary.size), format.number(summary.files))}</h1>
          <p class="mt-1 text-sm text-gray-400">
            {usage && `${t.freeNow(format.size(usage.free))} `}
            {summary.cloud > 0 && t.cloudOnly(format.size(summary.cloud))}
          </p>
        </div>
        <div class="flex gap-2">
          <LangToggle />
          <button
            type="button"
            onClick={onRescan}
            class={`cursor-pointer rounded-lg border border-gray-600 px-4 py-2 text-sm text-gray-200 hover:bg-gray-800 ${focusRing}`}
          >
            {t.rescan}
          </button>
        </div>
      </header>

      {summary.unreadable > 0 && !summary.elevated && (
        <p class="rounded-lg bg-amber-950/60 px-4 py-3 text-sm text-amber-200" role="note">
          {t.protectedFolders(format.number(summary.unreadable))}
          {uncounted >= GB && t.unexplained(format.size(uncounted))}
          {t.runAsAdmin}
        </p>
      )}

      <nav class="flex gap-1 border-b border-gray-800" aria-label={t.viewsLabel}>
        {tabs.map(({ id, label }) => (
          <button
            key={id}
            type="button"
            aria-current={tab === id ? "page" : undefined}
            onClick={() => setTab(id)}
            class={`cursor-pointer border-b-2 px-4 py-2 text-sm focus-visible:outline-2 focus-visible:outline-emerald-300 ${
              tab === id ? "border-emerald-400 text-white" : "border-transparent text-gray-400 hover:text-gray-200"
            }`}
          >
            {label}
          </button>
        ))}
      </nav>

      <div class="relative min-h-0 flex-1 overflow-y-auto">
        {tab === "files" && <EntryList entries={summary.topFiles ?? []} advice={summary.advice} showPath />}
        {tab === "dirs" && <EntryList entries={summary.topDirs ?? []} advice={summary.advice} showPath />}
        {tab === "dev" && <DevTrash findings={findings} safeSize={summary.dev.safeSize} />}
        {tab === "explore" && <Explorer root={summary.root} />}
      </div>

      {selected.size > 0 && (
        <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-gray-700 bg-gray-900 px-4 py-3" role="status">
          <p class="text-sm font-medium">{t.selectedSummary(format.number(selected.size), format.size(selectedSize))}</p>
          <div class="flex gap-2">
            <button
              type="button"
              onClick={() => setSelected(new Map())}
              class={`cursor-pointer rounded-lg border border-gray-600 px-4 py-2 text-sm text-gray-200 hover:bg-gray-800 ${focusRing}`}
            >
              {t.clearSelection}
            </button>
            <button
              type="button"
              onClick={() => setCleaning(Array.from(selected.keys()))}
              class={`cursor-pointer rounded-lg bg-emerald-400 px-5 py-2 text-sm font-semibold text-gray-950 hover:bg-emerald-300 ${focusRing}`}
            >
              {t.cleanSelected}
            </button>
          </div>
        </div>
      )}
    </main>
    {cleaning && (
      <CleanupDialog paths={cleaning} onClose={() => setCleaning(null)} onCleaned={onCleaned} onRescan={onRescan} />
    )}
    </Selection.Provider>
  );
}

function canClean(advice: Advice | undefined) {
  return !!advice && !advice.manual && (advice.level === "safe" || advice.level === "review");
}

/** Casilla de selección. Solo aparece en lo que la limpieza guiada ofrece. */
function SelectBox({ path, name, size, advice }: { path: string; name: string; size: number; advice: Advice | undefined }) {
  const { t } = useI18n();
  const { selected, toggle } = useContext(Selection);
  if (!canClean(advice)) return <span class="w-4 shrink-0" aria-hidden="true" />;
  return (
    <input
      type="checkbox"
      aria-label={t.selectItem(name)}
      checked={selected.has(path)}
      onChange={() => toggle(path, size)}
      class={`mt-1 h-4 w-4 shrink-0 cursor-pointer accent-emerald-400 ${focusRing}`}
    />
  );
}

function RevealButton({ path }: { path: string }) {
  const { t } = useI18n();
  return (
    <button
      type="button"
      aria-label={`${t.reveal}: ${path}`}
      title={t.reveal}
      onClick={() => CleanupService.Reveal(path).catch(console.error)}
      class={`mt-0.5 shrink-0 cursor-pointer rounded p-1 text-gray-500 hover:bg-gray-800 hover:text-gray-200 ${focusRing}`}
    >
      <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
      </svg>
    </button>
  );
}

function DevTrash({ findings, safeSize }: { findings: Finding[]; safeSize: number }) {
  const { t, format } = useI18n();
  const projects = findings.filter((finding) => finding.category === "project");
  const caches = findings.filter((finding) => finding.category === "cache");

  return (
    <>
      <div class="py-2">
        <h2 class="text-lg font-semibold">{safeSize > 0 ? t.devRecover(format.size(safeSize)) : t.devNothingSafe}</h2>
        <p class="text-sm text-gray-400">{t.devNote}</p>
      </div>
      <FindingList title={t.devCaches} findings={caches} />
      <FindingList title={t.devProjects} findings={projects} />
    </>
  );
}

function FindingList({ title, findings }: { title: string; findings: Finding[] }) {
  const { t, format } = useI18n();
  const { removed } = useContext(Selection);
  const visible = findings.filter((finding) => !removed.has(finding.path));
  if (visible.length === 0) return null;
  return (
    <section class="mt-3">
      <h3 class="px-2 text-sm font-medium text-gray-300">{title}</h3>
      <ul class="divide-y divide-gray-800/70">
        {visible.map((finding) => (
          <li key={finding.path} class="flex items-start gap-2 px-2 py-2">
            <SelectBox path={finding.path} name={finding.path} size={finding.size} advice={finding.advice} />
            <div class="min-w-0 flex-1">
              <div class="flex items-baseline gap-4">
                <span class="min-w-0 flex-1 truncate" title={finding.path}>
                  <code class="text-sm text-emerald-300">{finding.kind}</code>
                  {finding.project && <span class="text-gray-300"> · {finding.project}</span>}
                </span>
                <LevelChip advice={finding.advice} />
                <span class="shrink-0 font-medium tabular-nums">{format.size(finding.size)}</span>
              </div>
              <AdviceText advice={finding.advice} />
              {finding.lastActivity > 0 && (
                <p class="text-xs text-gray-500">{t.lastActivity(format.ago(finding.lastActivity))}</p>
              )}
            </div>
            <RevealButton path={finding.path} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function Explorer({ root }: { root: string }) {
  const { t, format } = useI18n();
  const [path, setPath] = useState(root);
  const [folder, setFolder] = useState<Folder | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let current = true;
    setFailed(false);
    ScanService.Listing(path)
      .then((next) => {
        if (current) setFolder(next);
      })
      .catch((err) => {
        console.error(err);
        if (current) setFailed(true);
      });
    return () => {
      current = false;
    };
  }, [path]);

  if (failed) return <p class="py-6 text-red-300">{t.folderOpenFailed}</p>;
  if (!folder) return <p class="py-6 text-gray-400">{t.loading}</p>;

  const entries = [...(folder.dirs ?? []), ...(folder.fileItems ?? [])].sort((a, b) => b.size - a.size);

  return (
    <>
      <div class="flex items-center gap-3 py-2">
        <button
          type="button"
          disabled={!folder.parent}
          onClick={() => setPath(folder.parent)}
          class="cursor-pointer rounded-md border border-gray-600 px-3 py-1 text-sm hover:bg-gray-800 focus-visible:outline-2 focus-visible:outline-emerald-300 disabled:cursor-not-allowed disabled:opacity-40"
        >
          {t.up}
        </button>
        <p class="min-w-0 truncate text-sm text-gray-300" title={folder.path}>
          {folder.path}
        </p>
        <p class="ml-auto shrink-0 text-sm text-gray-400">{format.size(folder.size)}</p>
      </div>
      {entries.length === 0 && folder.otherFiles === 0 ? (
        <p class="py-6 text-gray-400">{t.emptyFolder}</p>
      ) : (
        <EntryList entries={entries} advice={folder.advice} scale={folder.size} onOpen={(entry) => setPath(entry.path)} />
      )}
      {folder.otherFiles > 0 && (
        <p class="py-3 text-sm text-gray-400">{t.otherFiles(format.number(folder.otherFiles), format.size(folder.otherSize))}</p>
      )}
    </>
  );
}

function EntryList(props: {
  entries: Entry[];
  advice: AdviceMap;
  showPath?: boolean;
  scale?: number;
  onOpen?: (entry: Entry) => void;
}) {
  const { entries, advice, showPath, scale, onOpen } = props;
  const { t, format } = useI18n();
  const { removed } = useContext(Selection);
  const largest = scale || Math.max(1, ...entries.map((entry) => entry.size));

  function entrySize(entry: Entry) {
    if (entry.unreadable) return t.noAccess;
    if (entry.size === 0 && entry.cloud > 0) return t.inCloud;
    return format.size(entry.size);
  }

  return (
    <ul class="divide-y divide-gray-800/70">
      {entries.filter((entry) => !removed.has(entry.path)).map((entry) => {
        const entryAdvice = advice?.[entry.path];
        const content = (
          <>
            <div class="flex items-baseline gap-4">
              <span class="min-w-0 flex-1 truncate" title={entry.path}>
                {entry.isDir && <span class="sr-only">{t.folderPrefix}</span>}
                {entry.name}
                {entry.isDir && onOpen && <span class="text-gray-500"> ›</span>}
              </span>
              {entryAdvice && <LevelChip advice={entryAdvice} />}
              <span class={`shrink-0 tabular-nums ${entry.unreadable ? "text-gray-400" : "font-medium"}`}>{entrySize(entry)}</span>
            </div>
            {showPath && <p class="truncate text-xs text-gray-500">{entry.path}</p>}
            {entryAdvice && <AdviceText advice={entryAdvice} />}
            <div class="mt-1.5 h-1 overflow-hidden rounded-full bg-gray-800">
              <div class="h-full rounded-full bg-emerald-400/80" style={{ width: `${(entry.size / largest) * 100}%` }} />
            </div>
          </>
        );
        return (
          <li key={entry.path} class="flex items-start gap-2 px-2 py-2">
            <SelectBox path={entry.path} name={entry.name} size={entry.size} advice={entryAdvice} />
            {entry.isDir && onOpen ? (
              <button
                type="button"
                onClick={() => onOpen(entry)}
                class="block min-w-0 flex-1 cursor-pointer rounded text-left hover:bg-gray-800/60 focus-visible:outline-2 focus-visible:outline-emerald-300"
              >
                {content}
              </button>
            ) : (
              <div class="min-w-0 flex-1">{content}</div>
            )}
            <RevealButton path={entry.path} />
          </li>
        );
      })}
    </ul>
  );
}

const chipStyles: Record<string, string> = {
  safe: "bg-emerald-950 text-emerald-300",
  review: "bg-amber-950 text-amber-300",
  keep: "bg-red-950 text-red-300",
};

function LevelChip({ advice }: { advice: Advice }) {
  const { t } = useI18n();
  const labels: Record<string, string> = { safe: t.levelSafe, review: t.levelReview, keep: t.levelKeep };
  return (
    <span class={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${chipStyles[advice.level]}`}>{labels[advice.level]}</span>
  );
}

function AdviceText({ advice }: { advice: Advice }) {
  const { lang } = useI18n();
  const action = advice.action[lang];
  return (
    <p class="mt-0.5 text-xs text-gray-400">
      {advice.reason[lang]}
      {action && ` ${action}`}
    </p>
  );
}

function Bar({ percent, label }: { percent: number; label: string }) {
  return (
    <div
      class="mt-4 h-2 overflow-hidden rounded-full bg-gray-800"
      role="progressbar"
      aria-label={label}
      aria-valuenow={Math.round(percent)}
      aria-valuemin={0}
      aria-valuemax={100}
    >
      <div class="h-full rounded-full bg-emerald-400" style={{ width: `${percent}%` }} />
    </div>
  );
}

render(<App />, document.getElementById("app")!);
