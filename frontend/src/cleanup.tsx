import { useEffect, useRef, useState } from "preact/hooks";
import { CleanupService } from "../bindings/el-buscador";
import { useI18n } from "./i18n";

type Plan = Awaited<ReturnType<typeof CleanupService.Plan>>;
type PlanItem = NonNullable<Plan["items"]>[number];
type CleanStatus = Awaited<ReturnType<typeof CleanupService.Status>>;

const POLL_MS = 250;

export const focusRing = "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-emerald-300";
const secondaryButton = `cursor-pointer rounded-lg border border-gray-600 px-4 py-2 text-sm text-gray-200 hover:bg-gray-800 ${focusRing}`;
const primaryButton = `cursor-pointer rounded-lg bg-emerald-400 px-5 py-2 text-sm font-semibold text-gray-950 hover:bg-emerald-300 disabled:cursor-not-allowed disabled:opacity-40 ${focusRing}`;

/** Guía la limpieza: comprueba, pide confirmación, muestra el avance y el resultado. */
export function CleanupDialog(props: {
  paths: string[];
  onClose: () => void;
  onCleaned: (paths: string[]) => void;
  onRescan: () => void;
}) {
  const { paths, onClose, onCleaned, onRescan } = props;
  const { t } = useI18n();
  const [plan, setPlan] = useState<Plan | null>(null);
  const [failed, setFailed] = useState(false);
  const [status, setStatus] = useState<CleanStatus | null>(null);
  const reported = useRef(false);

  useEffect(() => {
    CleanupService.Plan(paths)
      .then(setPlan)
      .catch((err) => {
        console.error(err);
        setFailed(true);
      });
  }, []);

  const running = status?.state === "running";
  const done = status?.state === "done";

  useEffect(() => {
    if (!running) return;
    const timer = setInterval(() => {
      CleanupService.Status().then(setStatus).catch(console.error);
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [running]);

  useEffect(() => {
    if (!done || reported.current) return;
    reported.current = true;
    onCleaned((status.outcomes ?? []).filter((o) => o.status === "done" && o.method === "trash").map((o) => o.path));
  }, [done]);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape" && !running) onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [running]);

  async function start(items: PlanItem[]) {
    try {
      await CleanupService.Start(
        items.map((item) => ({ path: item.path, method: item.method, confirmedReview: item.advice.level === "review" })),
      );
      setStatus(await CleanupService.Status());
    } catch (err) {
      console.error(err);
      setFailed(true);
    }
  }

  let body;
  if (failed) {
    body = (
      <>
        <h2 id="clean-title" class="text-xl font-semibold">
          {t.planFailed}
        </h2>
        <div class="mt-4 flex justify-end">
          <AutoFocusButton class={secondaryButton} onClick={onClose} label={t.close} />
        </div>
      </>
    );
  } else if (status && done) {
    body = <Finished status={status} onClose={onClose} onRescan={onRescan} />;
  } else if (status) {
    body = <Running status={status} />;
  } else if (plan) {
    body = <Confirm plan={plan} onCancel={onClose} onConfirm={start} />;
  } else {
    body = (
      <h2 id="clean-title" class="text-xl font-semibold text-gray-300">
        {t.planLoading}
      </h2>
    );
  }

  return (
    <div class="fixed inset-0 z-10 flex items-center justify-center bg-black/70 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="clean-title"
        class="flex max-h-full w-full max-w-3xl flex-col rounded-xl border border-gray-700 bg-gray-900 p-5"
      >
        {body}
      </div>
    </div>
  );
}

function AutoFocusButton(props: { class: string; onClick: () => void; label: string }) {
  const ref = useRef<HTMLButtonElement>(null);
  useEffect(() => ref.current?.focus(), []);
  return (
    <button ref={ref} type="button" class={props.class} onClick={props.onClick}>
      {props.label}
    </button>
  );
}

function Confirm(props: { plan: Plan; onCancel: () => void; onConfirm: (items: PlanItem[]) => void }) {
  const { plan, onCancel, onConfirm } = props;
  const { t, format, lang } = useI18n();
  const [reviewed, setReviewed] = useState(false);

  const items = plan.items ?? [];
  const allowed = items.filter((item) => !item.code);
  const refused = items.filter((item) => item.code);
  const hasReview = allowed.some((item) => item.advice.level === "review");
  const hasSynced = allowed.some((item) => item.synced);

  return (
    <>
      <h2 id="clean-title" class="text-xl font-semibold">
        {allowed.length > 0 ? t.confirmTitle(format.size(plan.total)) : t.confirmNothing}
      </h2>
      {allowed.length > 0 && <p class="mt-1 text-sm text-gray-300">{t.confirmIntro}</p>}
      {hasSynced && (
        <p class="mt-3 rounded-lg bg-red-950/70 px-3 py-2 text-sm text-red-200" role="alert">
          {t.syncedWarning}
        </p>
      )}

      <div class="mt-3 min-h-0 flex-1 overflow-y-auto">
        <ul class="divide-y divide-gray-800">
          {allowed.map((item) => (
            <li key={item.path} class="py-2">
              <div class="flex items-baseline gap-3">
                <span class="min-w-0 flex-1 text-sm break-all text-gray-100">{item.path}</span>
                {item.advice.level === "review" && (
                  <span class="shrink-0 rounded-full bg-amber-950 px-2 py-0.5 text-xs font-medium text-amber-300">{t.levelReview}</span>
                )}
                <span class="shrink-0 text-sm font-medium tabular-nums">{format.size(item.size)}</span>
              </div>
              <p class="mt-0.5 text-xs text-gray-400">{item.advice.reason[lang]}</p>
              {item.method === "command" && (
                <p class="mt-1 text-xs text-gray-300">
                  {t.methodCommand} <code class="rounded bg-gray-800 px-1.5 py-0.5 text-emerald-300">{item.command}</code>
                </p>
              )}
              {item.missingTool && <p class="mt-1 text-xs text-amber-200">{t.toolMissing(item.missingTool)}</p>}
            </li>
          ))}
        </ul>

        {refused.length > 0 && (
          <section class="mt-3">
            <h3 class="text-sm font-medium text-gray-300">{t.refusedTitle}</h3>
            <ul class="divide-y divide-gray-800">
              {refused.map((item) => (
                <li key={item.path} class="py-2">
                  <p class="text-sm break-all text-gray-300">{item.path}</p>
                  <p class="text-xs text-amber-200">{t.refusal[item.code] ?? t.refusal.failed}</p>
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>

      {hasReview && (
        <label class="mt-3 flex cursor-pointer items-start gap-2 rounded-lg bg-amber-950/60 px-3 py-2 text-sm text-amber-100">
          <input
            type="checkbox"
            class="mt-0.5 accent-amber-300"
            checked={reviewed}
            onChange={(event) => setReviewed(event.currentTarget.checked)}
          />
          {t.reviewConfirm}
        </label>
      )}

      <div class="mt-4 flex justify-end gap-2">
        <AutoFocusButton class={secondaryButton} onClick={onCancel} label={allowed.length > 0 ? t.cancel : t.close} />
        {allowed.length > 0 && (
          <button type="button" class={primaryButton} disabled={hasReview && !reviewed} onClick={() => onConfirm(allowed)}>
            {t.confirmAction}
          </button>
        )}
      </div>
    </>
  );
}

function Running({ status }: { status: CleanStatus }) {
  const { t, format } = useI18n();
  const finished = status.outcomes?.length ?? 0;
  const current = Math.min(finished + 1, status.total);
  return (
    <>
      <div role="status">
        <h2 id="clean-title" class="text-xl font-semibold">
          {t.cleaningTitle(format.number(current), format.number(status.total))}
        </h2>
      </div>
      <p class="mt-2 text-sm break-all text-gray-400">{status.current || " "}</p>
      {status.output && <Output text={status.output} />}
      <p class="mt-3 text-xs text-gray-400">{t.stopNote}</p>
      <div class="mt-4 flex justify-end">
        <AutoFocusButton class={secondaryButton} onClick={() => CleanupService.Cancel().catch(console.error)} label={t.stop} />
      </div>
    </>
  );
}

function Finished(props: { status: CleanStatus; onClose: () => void; onRescan: () => void }) {
  const { status, onClose, onRescan } = props;
  const { t, format } = useI18n();
  const outcomes = status.outcomes ?? [];
  const problems = outcomes.filter((o) => o.status !== "done");
  const stopped = outcomes.some((o) => o.status === "canceled");
  const commands = outcomes.filter((o) => o.status === "done" && o.method === "command").length;

  return (
    <>
      <div role="status">
        <h2 id="clean-title" class="text-xl font-semibold">
          {stopped ? t.stoppedTitle : t.doneTitle}
        </h2>
      </div>
      <div class="mt-2 space-y-1 text-sm text-gray-200">
        {status.trashed > 0 && <p>{t.doneTrashed(format.size(status.trashed))}</p>}
        {commands > 0 && <p>{t.doneCommands(format.number(commands))}</p>}
        {status.freedOk && <p>{status.freed > 0 ? t.doneFreed(format.size(status.freed)) : t.doneNoFree}</p>}
      </div>

      <div class="mt-3 min-h-0 flex-1 overflow-y-auto">
        {problems.length > 0 && (
          <section>
            <h3 class="text-sm font-medium text-gray-300">{t.doneFailed}</h3>
            <ul class="divide-y divide-gray-800">
              {problems.map((outcome) => (
                <li key={outcome.path} class="py-2">
                  <p class="text-sm break-all text-gray-300">{outcome.path}</p>
                  <p class="text-xs text-amber-200">{t.refusal[outcome.code] ?? t.refusal.failed}</p>
                </li>
              ))}
            </ul>
          </section>
        )}
        {status.output && (
          <section class="mt-3">
            <h3 class="text-sm font-medium text-gray-300">{t.doneCommandOutput}</h3>
            <Output text={status.output} />
          </section>
        )}
      </div>

      <div class="mt-4 flex flex-wrap justify-end gap-2">
        {status.trashed > 0 && (
          <button type="button" class={secondaryButton} onClick={() => CleanupService.OpenRecycleBin().catch(console.error)}>
            {t.openBin}
          </button>
        )}
        <button type="button" class={secondaryButton} onClick={onRescan}>
          {t.rescan}
        </button>
        <AutoFocusButton class={primaryButton} onClick={onClose} label={t.close} />
      </div>
    </>
  );
}

function Output({ text }: { text: string }) {
  return (
    <pre class="mt-2 max-h-40 overflow-auto rounded-lg bg-gray-950 p-3 text-xs whitespace-pre-wrap text-gray-300" dir="ltr">
      {text}
    </pre>
  );
}
