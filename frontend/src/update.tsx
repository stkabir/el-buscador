import { useEffect, useState } from "preact/hooks";
import { UpdateService } from "../bindings/el-buscador";
import { focusRing } from "./cleanup";
import { useI18n } from "./i18n";

type Status = Awaited<ReturnType<typeof UpdateService.Status>>;

const POLL_MS = 500;

/** Avisa si hay versión nueva. Nada se instala sin que el usuario lo pida. */
export function UpdateBar() {
  const { t, format } = useI18n();
  const [status, setStatus] = useState<Status | null>(null);
  const [hidden, setHidden] = useState(false);

  useEffect(() => {
    // Revisar no debe estorbar el arranque, y sin internet no pasa nada.
    UpdateService.Check().then(setStatus).catch(console.error);
  }, []);

  const installing = status?.state === "installing";
  const active = status?.state === "available" || status?.state === "failed" || installing;
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => {
      UpdateService.Status().then(setStatus).catch(console.error);
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [active]);

  if (!status || !active || hidden) return null;

  const percent = status.total > 0 ? Math.round((status.written / status.total) * 100) : 0;

  async function install() {
    try {
      await UpdateService.Install();
      setStatus(await UpdateService.Status());
    } catch (err) {
      console.error(err);
    }
  }

  const button = `shrink-0 cursor-pointer rounded-md border border-emerald-400/60 px-3 py-1 text-xs font-medium hover:bg-emerald-400/10 disabled:cursor-not-allowed disabled:opacity-40 ${focusRing}`;

  return (
    <div class="flex flex-wrap items-center gap-3 bg-emerald-950 px-6 py-2 text-sm text-emerald-100" role="status">
      {installing ? (
        <p class="flex-1">{status.total > 0 && percent < 100 ? t.updateDownloading(format.number(percent)) : t.updateRestarting}</p>
      ) : status.state === "failed" ? (
        <>
          <p class="min-w-0 flex-1">{t.updateFailed}</p>
          <button type="button" class={button} disabled={status.busy} onClick={install}>
            {t.updateRetry}
          </button>
        </>
      ) : (
        <>
          <p class="min-w-0 flex-1">
            {t.updateAvailable(status.available)}
            {status.busy && ` ${t.updateBusy}`}
          </p>
          <button type="button" class={button} disabled={status.busy} onClick={install}>
            {t.updateAction}
          </button>
        </>
      )}
      {!installing && (
        <button type="button" class={`shrink-0 cursor-pointer rounded px-2 py-1 text-xs text-emerald-200 hover:bg-emerald-400/10 ${focusRing}`} onClick={() => setHidden(true)}>
          {t.updateLater}
        </button>
      )}
    </div>
  );
}

/** Muestra la versión instalada, útil para reportar un problema. */
export function VersionNote() {
  const { t } = useI18n();
  const [version, setVersion] = useState("");
  useEffect(() => {
    UpdateService.Status()
      .then((status) => setVersion(status.version))
      .catch(console.error);
  }, []);
  if (!version) return null;
  return <p class="mt-2 text-xs text-gray-500">{version === "dev" ? t.versionDev : t.version(version)}</p>;
}
