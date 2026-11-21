import { createPortal } from "preact/compat";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { DuplicateService } from "../bindings/el-buscador";
import { BinOver, Finished, RevealButton, Running, focusRing, primaryButton, secondaryButton } from "./cleanup";
import { useI18n } from "./i18n";

type Report = Awaited<ReturnType<typeof DuplicateService.Report>>;
type Group = NonNullable<Report["groups"]>[number];
type Copy = NonNullable<Group["copies"]>[number];
type SearchStatus = Awaited<ReturnType<typeof DuplicateService.Status>>;
type CleanStatus = Awaited<ReturnType<typeof DuplicateService.CleanStatus>>;
type BinSpace = Awaited<ReturnType<typeof DuplicateService.BinSpace>>;

const POLL_MS = 250;
const PAGE = 50;

function folderOf(path: string) {
  const index = path.lastIndexOf("\\");
  return index > 0 ? path.slice(0, index) : path;
}

function nameOf(path: string) {
  return path.slice(path.lastIndexOf("\\") + 1);
}

/** Lo que el usuario decidió en un grupo: las copias que se quedan. */
type Kept = Record<string, string[]>;

function copiesOf(group: Group): Copy[] {
  return group.copies ?? [];
}

/** Lo que se borraría: lo que no se conserva y no está bloqueado. */
function toDelete(group: Group, kept: string[] | undefined): Copy[] {
  if (!kept || kept.length === 0) return [];
  return copiesOf(group).filter((copy) => !copy.locked && !kept.includes(copy.path));
}

export function Duplicates(props: { onDialog: (open: boolean) => void; onCleaned: (paths: string[]) => void; onRescan: () => void }) {
  const { onDialog, onCleaned, onRescan } = props;
  const { t, format } = useI18n();
  const [status, setStatus] = useState<SearchStatus | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [failed, setFailed] = useState(false);
  const [kept, setKept] = useState<Kept>({});
  const [shown, setShown] = useState(PAGE);
  const [confirming, setConfirming] = useState(false);
  const [folders, setFolders] = useState<string[]>([]);

  const searching = status?.state === "searching";

  async function refresh(next: SearchStatus) {
    if (next.state === "done") setReport(await DuplicateService.Report());
    setStatus(next);
  }

  // Si ya hay una búsqueda de antes, la muestra en vez de ofrecer otra.
  useEffect(() => {
    DuplicateService.Status()
      .then((next) => (next.state === "idle" ? undefined : refresh(next)))
      .catch(console.error);
  }, []);

  useEffect(() => {
    if (!searching) return;
    const timer = setInterval(() => {
      DuplicateService.Status().then(refresh).catch(console.error);
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [searching]);

  async function start() {
    setFailed(false);
    setReport(null);
    setKept({});
    setShown(PAGE);
    try {
      await DuplicateService.Start(folders);
      await refresh(await DuplicateService.Status());
    } catch (err) {
      console.error(err);
      setFailed(true);
    }
  }

  function keepOne(group: Group, path: string) {
    setKept((current) => ({ ...current, [group.hash]: [path] }));
  }

  // Solo marca: nada se toca hasta la confirmación. Incluye subcarpetas, porque las
  // fotos y los respaldos suelen ser árboles. Un grupo sin copias ahí queda como estaba,
  // porque no hay nada que garantice una copia.
  function keepFolder(folder: string) {
    const prefix = `${folder}\\`.toLowerCase();
    setKept((current) => {
      const next = { ...current };
      for (const group of report?.groups ?? []) {
        const inFolder = copiesOf(group)
          .filter((copy) => copy.path.toLowerCase().startsWith(prefix))
          .map((copy) => copy.path);
        if (inFolder.length > 0) next[group.hash] = inFolder;
      }
      return next;
    });
  }

  // La carpeta elegida para comparar dice más que la inmediata: "Conservar lo de
  // Fototeca" y no "de 2019".
  function keepRootOf(path: string) {
    const lower = path.toLowerCase();
    return folders.find((folder) => lower.startsWith(`${folder}\\`.toLowerCase())) ?? folderOf(path);
  }

  async function addFolder() {
    try {
      const folder = await DuplicateService.ChooseFolder(t.dupChooseTitle);
      if (folder && !folders.includes(folder)) setFolders([...folders, folder]);
    } catch (err) {
      console.error(err);
      setFailed(true);
    }
  }

  // Deja marcados los grupos que caben, del que más libera al que menos. Quita grupos
  // completos: es lo que el usuario decidió junto.
  function trimToRoom(room: number) {
    setKept((current) => {
      const next = { ...current };
      let left = room;
      for (const group of report?.groups ?? []) {
        const size = toDelete(group, current[group.hash]).length * group.size;
        if (size === 0) continue;
        if (size <= left) left -= size;
        else delete next[group.hash];
      }
      return next;
    });
  }

  const groups = report?.groups ?? [];
  const selection = useMemo(() => {
    let count = 0;
    let size = 0;
    for (const group of groups) {
      const copies = toDelete(group, kept[group.hash]);
      count += copies.length;
      size += copies.length * group.size;
    }
    return { count, size };
  }, [groups, kept]);

  if (!status || status.state === "idle") {
    return <Intro failed={failed} folders={folders} onAdd={addFolder} onRemove={(folder) => setFolders(folders.filter((item) => item !== folder))} onStart={start} />;
  }
  if (searching) return <Searching status={status} />;
  if (!report) return <p class="py-6 text-gray-400">{t.loading}</p>;

  return (
    <div class="flex h-full flex-col">
      <div class="py-2">
        <h2 class="text-lg font-semibold">
          {groups.length > 0 ? t.dupFound(format.size(report.reclaimable), format.number(groups.length)) : t.dupNone}
        </h2>
        <p class="text-sm text-gray-400">{t.dupIdentical}</p>
        <button
          type="button"
          class={`${secondaryButton} mt-2 !px-3 !py-1 !text-xs`}
          onClick={() => {
            setReport(null);
            setKept({});
            setStatus({ ...status, state: "idle" });
          }}
        >
          {folders.length > 0 ? t.dupChangeFolders : t.dupChooseFolders}
        </button>
        {report.canceled && (
          <p class="mt-2 flex flex-wrap items-center gap-3 text-sm text-amber-200">
            {t.dupStopped}
            <button type="button" onClick={start} class={secondaryButton}>
              {t.dupContinue}
            </button>
          </p>
        )}
        {report.cloudFiles > 0 && (
          <p class="mt-2 rounded-lg bg-amber-950/60 px-3 py-2 text-sm text-amber-200" role="note">
            {t.dupCloudSkipped(format.number(report.cloudFiles), format.size(report.cloudSize))}
          </p>
        )}
        {report.unreadable > 0 && <p class="mt-2 text-sm text-gray-400">{t.dupUnreadable(format.number(report.unreadable))}</p>}
      </div>

      <ul class="divide-y divide-gray-800/70">
        {groups.slice(0, shown).map((group) => (
          <GroupRow key={group.hash} group={group} kept={kept[group.hash]} keepRootOf={keepRootOf} onKeep={keepOne} onKeepFolder={keepFolder} />
        ))}
      </ul>
      {groups.length > shown && (
        <button type="button" class={`${secondaryButton} my-3 self-center`} onClick={() => setShown(shown + PAGE)}>
          {t.dupShowMore(format.number(groups.length - shown))}
        </button>
      )}

      {selection.count > 0 && (
        <div class="sticky bottom-0 mt-3 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-gray-700 bg-gray-900 px-4 py-3" role="status">
          <p class="text-sm font-medium">{t.selectedSummary(format.number(selection.count), format.size(selection.size))}</p>
          <div class="flex gap-2">
            <button type="button" class={secondaryButton} onClick={() => setKept({})}>
              {t.clearSelection}
            </button>
            <button
              type="button"
              class={primaryButton}
              onClick={() => {
                setConfirming(true);
                onDialog(true);
              }}
            >
              {t.cleanSelected}
            </button>
          </div>
        </div>
      )}

      {confirming &&
        createPortal(
          <CleanDialog
            groups={groups}
            kept={kept}
            onTrim={trimToRoom}
            onClose={async () => {
              setConfirming(false);
              onDialog(false);
              setReport(await DuplicateService.Report());
            }}
            onCleaned={(paths) => {
              onCleaned(paths);
              setKept((current) => {
                const next = { ...current };
                for (const group of groups) {
                  if (copiesOf(group).some((copy) => paths.includes(copy.path))) delete next[group.hash];
                }
                return next;
              });
            }}
            onRescan={() => {
              setConfirming(false);
              onDialog(false);
              onRescan();
            }}
          />,
          document.body,
        )}
    </div>
  );
}

function Intro(props: { failed: boolean; folders: string[]; onAdd: () => void; onRemove: (folder: string) => void; onStart: () => void }) {
  const { failed, folders, onAdd, onRemove, onStart } = props;
  const { t } = useI18n();
  return (
    <div class="flex flex-col items-center gap-4 py-10 text-center">
      <h2 class="text-xl font-semibold">{t.dupIntroTitle}</h2>
      <p class="max-w-lg text-sm text-gray-300">{t.dupIntroBody}</p>
      <button
        type="button"
        onClick={onStart}
        class={`cursor-pointer rounded-xl bg-emerald-400 px-6 py-3 font-semibold text-gray-950 hover:bg-emerald-300 ${focusRing}`}
      >
        {folders.length > 0 ? t.dupStartFolders : t.dupStart}
      </button>
      <div class="flex w-full max-w-lg flex-col items-center gap-2 text-sm text-gray-400">
        {folders.length === 0 && <p>{t.dupFoldersOptional}</p>}
        {folders.map((folder) => (
          <p key={folder} class="flex w-full items-center gap-2 rounded-lg border border-gray-700 px-3 py-1.5 text-left">
            <span class="min-w-0 flex-1 break-all text-gray-200">{folder}</span>
            <button
              type="button"
              aria-label={`${t.dupRemoveFolder}: ${folder}`}
              onClick={() => onRemove(folder)}
              class={`shrink-0 cursor-pointer rounded px-2 text-gray-400 hover:bg-gray-800 hover:text-gray-200 ${focusRing}`}
            >
              ✕
            </button>
          </p>
        ))}
        <button type="button" class={`${secondaryButton} !px-3 !py-1 !text-xs`} onClick={onAdd}>
          {t.dupAddFolder}
        </button>
      </div>
      {failed && <p class="text-sm text-red-300">{t.dupFailed}</p>}
    </div>
  );
}

function Searching({ status }: { status: SearchStatus }) {
  const { t, format } = useI18n();
  const { phase, done, total, bytes, bytesGoal, current } = status.progress;
  const full = phase === "full";
  return (
    <div class="flex flex-col items-center gap-4 py-10 text-center">
      <div role="status">
        <h2 class="text-xl font-semibold">{full ? t.dupComparing : t.dupNarrowing}</h2>
        <p class="mt-2 text-sm text-gray-400">
          {t.dupProgress(format.number(done), format.number(total))}
          {full && bytesGoal > 0 && ` · ${format.size(bytes)} / ${format.size(bytesGoal)}`}
        </p>
      </div>
      <p class="w-full max-w-2xl truncate text-sm text-gray-500" dir="ltr" title={current}>
        {current || " "}
      </p>
      <button type="button" class={secondaryButton} onClick={() => DuplicateService.Cancel().catch(console.error)}>
        {t.stop}
      </button>
    </div>
  );
}

function GroupRow(props: { group: Group; kept: string[] | undefined; keepRootOf: (path: string) => string; onKeep: (group: Group, path: string) => void; onKeepFolder: (folder: string) => void }) {
  const { group, kept, keepRootOf, onKeep, onKeepFolder } = props;
  const { t, format } = useI18n();
  const copies = copiesOf(group);
  const lockedText: Record<string, string> = { tracked: t.dupLockedGit, keep: t.dupLockedKeep, manual: t.dupLockedManual };
  return (
    <li class="px-2 py-3">
      <div class="flex items-baseline gap-4">
        <span class="min-w-0 flex-1 truncate font-medium" title={copies[0]?.path}>
          {nameOf(copies[0]?.path ?? "")}
        </span>
        <span class="shrink-0 text-sm text-gray-400">{t.dupCopies(format.number(copies.length), format.size(group.size))}</span>
        <span class="shrink-0 font-medium tabular-nums">{format.size(group.reclaimable)}</span>
      </div>
      {/* Radios sin name: "conservar lo de esta carpeta" puede marcar varias copias, y
          un mismo name solo permitiría mostrar una. */}
      <fieldset class="mt-1">
        <legend class="sr-only">{t.dupKeepLegend(nameOf(copies[0]?.path ?? ""))}</legend>
        {copies.map((copy) => {
          const keepRoot = keepRootOf(copy.path);
          const isKept = kept?.includes(copy.path) ?? false;
          const deleting = !!kept && kept.length > 0 && !isKept && !copy.locked;
          return (
            <div key={copy.path} class="flex items-start gap-2 py-1">
              <input
                type="radio"
                aria-label={`${t.dupKeep}: ${copy.path}`}
                checked={isKept}
                onChange={() => onKeep(group, copy.path)}
                class={`mt-1 h-4 w-4 shrink-0 cursor-pointer accent-emerald-400 ${focusRing}`}
              />
              <div class="min-w-0 flex-1">
                <p class={`text-sm break-all ${deleting ? "text-gray-500 line-through" : "text-gray-200"}`}>{copy.path}</p>
                <p class="text-xs text-gray-500">
                  {t.dupModified(format.ago(copy.modified))}
                  {isKept && <span class="text-emerald-300"> · {t.dupKeeping}</span>}
                  {copy.locked && <span class="text-amber-200"> · {lockedText[copy.locked] ?? t.dupLockedKeep}</span>}
                  {copy.synced && <span class="text-amber-200"> · {t.dupSynced}</span>}
                </p>
              </div>
              <button
                type="button"
                onClick={() => onKeepFolder(keepRoot)}
                class={`max-w-44 shrink-0 cursor-pointer truncate rounded px-2 py-1 text-xs text-gray-400 hover:bg-gray-800 hover:text-gray-200 ${focusRing}`}
                title={t.dupKeepFolderTitle(keepRoot)}
              >
                {t.dupKeepFolder(nameOf(keepRoot))}
              </button>
              <RevealButton path={copy.path} onReveal={(path) => DuplicateService.Reveal(path)} />
            </div>
          );
        })}
      </fieldset>
    </li>
  );
}

function CleanDialog(props: { groups: Group[]; kept: Kept; onTrim: (room: number) => void; onClose: () => void; onCleaned: (paths: string[]) => void; onRescan: () => void }) {
  const { groups, kept, onTrim, onClose, onCleaned, onRescan } = props;
  const { t, format } = useI18n();
  const [reviewed, setReviewed] = useState(false);
  const [bin, setBin] = useState<BinSpace | null>(null);
  const [status, setStatus] = useState<CleanStatus | null>(null);
  const [failed, setFailed] = useState(false);
  const reported = useRef(false);
  const cancelRef = useRef<HTMLButtonElement>(null);

  const decided = groups
    .map((group) => ({ group, keep: kept[group.hash] ?? [], remove: toDelete(group, kept[group.hash]) }))
    .filter((item) => item.remove.length > 0);
  const total = decided.reduce((sum, item) => sum + item.remove.length * item.group.size, 0);
  const synced = decided.some((item) => item.remove.some((copy) => copy.synced));

  const running = status?.state === "running";
  const done = status?.state === "done";

  useEffect(() => cancelRef.current?.focus(), []);

  useEffect(() => {
    DuplicateService.BinSpace().then(setBin).catch(console.error);
  }, []);

  const overBin = !!bin && bin.known && total > bin.room;
  // El recorte solo sirve si algún grupo completo cabe.
  const canTrim = overBin && decided.some((item) => item.remove.length * item.group.size <= bin.room);

  useEffect(() => {
    if (!running) return;
    const timer = setInterval(() => {
      DuplicateService.CleanStatus().then(setStatus).catch(console.error);
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [running]);

  useEffect(() => {
    if (!done || reported.current || !status) return;
    reported.current = true;
    onCleaned((status.outcomes ?? []).filter((o) => o.status === "done").map((o) => o.path));
  }, [done]);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape" && !running) onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [running]);

  async function start() {
    try {
      await DuplicateService.Clean(decided.map((item) => ({ keep: item.keep[0], delete: item.remove.map((copy) => copy.path) })));
      setStatus(await DuplicateService.CleanStatus());
    } catch (err) {
      console.error(err);
      setFailed(true);
    }
  }

  let body;
  if (status && done) {
    body = <Finished status={status} onClose={onClose} onRescan={onRescan} />;
  } else if (status) {
    body = <Running status={status} onStop={() => DuplicateService.CancelClean().catch(console.error)} />;
  } else {
    body = (
      <>
        <h2 id="clean-title" class="text-xl font-semibold">
          {t.confirmTitle(format.size(total))}
        </h2>
        <p class="mt-1 text-sm text-gray-300">{t.dupConfirmIntro}</p>
        {synced && (
          <p class="mt-3 rounded-lg bg-red-950/70 px-3 py-2 text-sm text-red-200" role="alert">
            {t.syncedWarning}
          </p>
        )}
        {failed && <p class="mt-3 text-sm text-red-300">{t.dupFailed}</p>}
        {overBin && bin && <BinOver total={total} room={bin.room} onTrim={canTrim ? () => onTrim(bin.room) : undefined} />}
        <div class="mt-3 min-h-0 flex-1 overflow-y-auto">
          <ul class="divide-y divide-gray-800">
            {decided.map((item) => (
              <li key={item.group.hash} class="py-2">
                {item.keep.map((path) => (
                  <p key={path} class="text-sm break-all text-emerald-300">
                    {t.dupKeeping}: {path}
                  </p>
                ))}
                {item.remove.map((copy) => (
                  <p key={copy.path} class="mt-0.5 text-sm break-all text-gray-200">
                    {t.dupRemoving}: {copy.path} <span class="tabular-nums text-gray-400">({format.size(item.group.size)})</span>
                  </p>
                ))}
              </li>
            ))}
          </ul>
        </div>
        <label class="mt-3 flex cursor-pointer items-start gap-2 rounded-lg bg-amber-950/60 px-3 py-2 text-sm text-amber-100">
          <input type="checkbox" class="mt-0.5 accent-amber-300" checked={reviewed} onChange={(event) => setReviewed(event.currentTarget.checked)} />
          {t.dupReviewConfirm}
        </label>
        <div class="mt-4 flex justify-end gap-2">
          <button ref={cancelRef} type="button" class={secondaryButton} onClick={onClose}>
            {t.cancel}
          </button>
          <button type="button" class={primaryButton} disabled={!reviewed || overBin || !bin} onClick={start}>
            {t.confirmAction}
          </button>
        </div>
      </>
    );
  }

  return (
    <div class="fixed inset-0 z-10 flex items-center justify-center bg-black/70 p-4">
      <div role="dialog" aria-modal="true" aria-labelledby="clean-title" class="flex max-h-full w-full max-w-3xl flex-col rounded-xl border border-gray-700 bg-gray-900 p-5">
        {body}
      </div>
    </div>
  );
}
