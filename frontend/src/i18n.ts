import { createContext } from "preact";
import { useContext } from "preact/hooks";

export type Lang = "es" | "en";

const es = {
  checkingDisk: "Revisando tu disco…",
  checkingProjects: "Revisando tus proyectos…",
  diskReadFailed: "No pudimos leer tu disco",
  diskFree: (drive: string, free: string, total: string) => `Tu disco ${drive} tiene ${free} libres de ${total}`,
  spaceUsed: "Espacio usado",
  scanButton: "Ver lo que más pesa",
  canceledNote: "Cancelaste la búsqueda. No se cambió nada.",
  failedNote: "No pudimos terminar la búsqueda. Inténtalo de nuevo.",
  promise: "Solo mira tu disco. No borra nada sin preguntarte.",
  progress: (files: string, size: string) => `${files} archivos, ${size}`,
  cancel: "Cancelar",
  found: (size: string, files: string) => `Encontramos ${size} en ${files} archivos`,
  freeNow: (free: string) => `Tu disco tiene ${free} libres.`,
  cloudOnly: (size: string) => `Otros ${size} están solo en la nube y no ocupan tu disco.`,
  rescan: "Buscar de nuevo",
  protectedFolders: (count: string) => `No pudimos abrir ${count} carpetas protegidas`,
  unexplained: (size: string) => `, y faltan por explicar ${size} del espacio usado`,
  runAsAdmin: ". Abre la app como administrador para ver más.",
  viewsLabel: "Vistas de resultados",
  tabFiles: "Archivos",
  tabFolders: "Carpetas",
  tabDev: "Basura dev",
  tabExplore: "Explorar",
  folderOpenFailed: "No pudimos abrir esta carpeta.",
  loading: "Cargando…",
  up: "Subir",
  emptyFolder: "Esta carpeta está vacía.",
  otherFiles: (count: string, size: string) => `Y otros ${count} archivos más pequeños: ${size}`,
  folderPrefix: "Carpeta: ",
  noAccess: "Sin acceso",
  inCloud: "En la nube",
  levelSafe: "Seguro",
  levelReview: "Revisar",
  levelKeep: "No tocar",
  devRecover: (size: string) => `Puedes recuperar hasta ${size}`,
  devNothingSafe: "Nada marcado como seguro",
  devNote: "Suma solo lo marcado como Seguro. Marca lo que quieras limpiar: te preguntaremos antes de tocar nada.",
  devProjects: "Proyectos",
  devCaches: "Cachés de herramientas",
  lastActivity: (when: string) => `Última actividad ${when}`,
  switchLang: "Switch to English",
  switchLangShort: "EN",
  selectItem: (name: string) => `Seleccionar ${name}`,
  reveal: "Abrir en el Explorador",
  selectedSummary: (count: string, size: string) => `${count} seleccionados, ${size}`,
  clearSelection: "Quitar selección",
  cleanSelected: "Limpiar…",
  planLoading: "Comprobando lo que seleccionaste…",
  planFailed: "No pudimos comprobar lo que seleccionaste.",
  confirmTitle: (size: string) => `¿Limpiar ${size}?`,
  confirmIntro: "Se manda a la papelera, así que puedes restaurarlo. Revisa la lista:",
  confirmNothing: "Nada de lo que seleccionaste se puede limpiar.",
  methodCommand: "Se limpia con su comando oficial, que no pasa por la papelera:",
  toolMissing: (tool: string) => `${tool} no está instalado, así que se manda a la papelera.`,
  syncedWarning: "Algunos elementos se sincronizan: borrarlos también los borra de la nube y de tus otros dispositivos.",
  reviewTitle: "Necesitan tu revisión",
  reviewConfirm: "Revisé los elementos marcados con Revisar y quiero limpiarlos.",
  refusedTitle: "No se van a limpiar",
  confirmAction: "Limpiar",
  close: "Cerrar",
  cleaningTitle: (done: string, total: string) => `Limpiando ${done} de ${total}…`,
  stop: "Detener",
  stopNote: "Lo que ya llegó a la papelera ahí se queda, y puedes restaurarlo.",
  doneTitle: "Limpieza terminada",
  stoppedTitle: "Limpieza detenida",
  doneTrashed: (size: string) => `En la papelera: ${size}. Ese espacio se libera cuando la vacías.`,
  doneFreed: (size: string) => `Tu disco ganó ${size} en total.`,
  doneNoFree: "Tu disco todavía no ganó espacio.",
  openBin: "Abrir la papelera",
  doneFailed: "No se limpiaron",
  doneCommandOutput: "Salida de los comandos",
  doneCommands: (count: string) => `Comandos ejecutados: ${count}.`,
  refusal: {
    notScanned: "Ya no está en el último escaneo. Busca de nuevo.",
    invalidPath: "Esta ubicación no se puede limpiar desde aquí.",
    protected: "Es una carpeta de sistema o personal, y está protegida.",
    missing: "Ya no existe.",
    link: "Es un enlace a otra ubicación.",
    changed: "Cambió desde el último escaneo. Busca de nuevo.",
    keep: "Ahora está marcado como No tocar.",
    unclassified: "Ya no sabemos con seguridad qué es.",
    manual: "Se quita desde su propia app o desde Configuración, no borrándolo.",
    needsConfirm: "Falta confirmar la revisión.",
    tooBig: "Es demasiado grande para la papelera. No se tocó: nunca lo borramos de forma permanente.",
    binOff: "Tu papelera está desactivada en este disco. No se tocó: nunca lo borramos de forma permanente.",
    binRejected: "La papelera no lo aceptó. No se tocó: nunca lo borramos de forma permanente.",
    inUse: "Otro programa lo está usando. Ciérralo e inténtalo de nuevo.",
    noPermission: "No tienes permiso para moverlo.",
    toolMissing: "La herramienta ya no está instalada. No se tocó.",
    toolFailed: "El comando terminó con error. Revisa su salida.",
    canceled: "Se detuvo antes de llegar a este elemento.",
    failed: "Algo salió mal y no se limpió.",
  } as Record<string, string>,
};

const en: typeof es = {
  checkingDisk: "Checking your disk…",
  checkingProjects: "Checking your projects…",
  diskReadFailed: "We couldn't read your disk",
  diskFree: (drive, free, total) => `Your ${drive} drive has ${free} free of ${total}`,
  spaceUsed: "Space used",
  scanButton: "See what's taking up space",
  canceledNote: "You canceled the search. Nothing changed.",
  failedNote: "We couldn't finish the search. Please try again.",
  promise: "It only looks at your disk. It won't delete anything without asking.",
  progress: (files, size) => `${files} files, ${size}`,
  cancel: "Cancel",
  found: (size, files) => `We found ${size} in ${files} files`,
  freeNow: (free) => `Your disk has ${free} free.`,
  cloudOnly: (size) => `Another ${size} is cloud-only and doesn't use your disk.`,
  rescan: "Search again",
  protectedFolders: (count) => `We couldn't open ${count} protected folders`,
  unexplained: (size) => `, and ${size} of used space is unaccounted for`,
  runAsAdmin: ". Open the app as administrator to see more.",
  viewsLabel: "Result views",
  tabFiles: "Files",
  tabFolders: "Folders",
  tabDev: "Dev junk",
  tabExplore: "Explore",
  folderOpenFailed: "We couldn't open this folder.",
  loading: "Loading…",
  up: "Up",
  emptyFolder: "This folder is empty.",
  otherFiles: (count, size) => `And ${count} smaller files: ${size}`,
  folderPrefix: "Folder: ",
  noAccess: "No access",
  inCloud: "In the cloud",
  levelSafe: "Safe",
  levelReview: "Review",
  levelKeep: "Don't touch",
  devRecover: (size) => `You can recover up to ${size}`,
  devNothingSafe: "Nothing marked as safe",
  devNote: "Only counts items marked Safe. Check what you want to clean: we'll ask before touching anything.",
  devProjects: "Projects",
  devCaches: "Tool caches",
  lastActivity: (when) => `Last activity ${when}`,
  switchLang: "Cambiar a español",
  switchLangShort: "ES",
  selectItem: (name) => `Select ${name}`,
  reveal: "Open in File Explorer",
  selectedSummary: (count, size) => `${count} selected, ${size}`,
  clearSelection: "Clear selection",
  cleanSelected: "Clean…",
  planLoading: "Checking what you selected…",
  planFailed: "We couldn't check what you selected.",
  confirmTitle: (size) => `Clean ${size}?`,
  confirmIntro: "It goes to the Recycle Bin, so you can restore it. Check the list:",
  confirmNothing: "Nothing you selected can be cleaned.",
  methodCommand: "Cleaned with its official command, which skips the Recycle Bin:",
  toolMissing: (tool) => `${tool} isn't installed, so this goes to the Recycle Bin.`,
  syncedWarning: "Some items sync: deleting them also deletes them from the cloud and your other devices.",
  reviewTitle: "These need your review",
  reviewConfirm: "I reviewed the items marked Review and want to clean them.",
  refusedTitle: "These won't be cleaned",
  confirmAction: "Clean",
  close: "Close",
  cleaningTitle: (done, total) => `Cleaning ${done} of ${total}…`,
  stop: "Stop",
  stopNote: "What already reached the Recycle Bin stays there, and you can restore it.",
  doneTitle: "Cleanup finished",
  stoppedTitle: "Cleanup stopped",
  doneTrashed: (size) => `In the Recycle Bin: ${size}. That space is freed when you empty it.`,
  doneFreed: (size) => `Your disk gained ${size} in total.`,
  doneNoFree: "Your disk hasn't gained space yet.",
  openBin: "Open Recycle Bin",
  doneFailed: "Not cleaned",
  doneCommandOutput: "Command output",
  doneCommands: (count) => `Commands run: ${count}.`,
  refusal: {
    notScanned: "It's no longer in the last scan. Search again.",
    invalidPath: "This location can't be cleaned from here.",
    protected: "It's a system or personal folder, and it's protected.",
    missing: "It no longer exists.",
    link: "It's a link to another location.",
    changed: "It changed since the last scan. Search again.",
    keep: "It's now marked Don't touch.",
    unclassified: "We no longer know for sure what it is.",
    manual: "Remove it from its own app or from Settings, not by deleting it.",
    needsConfirm: "The review confirmation is missing.",
    tooBig: "It's too big for the Recycle Bin. It wasn't touched: we never delete permanently.",
    binOff: "Your Recycle Bin is turned off on this disk. It wasn't touched: we never delete permanently.",
    binRejected: "The Recycle Bin didn't accept it. It wasn't touched: we never delete permanently.",
    inUse: "Another program is using it. Close it and try again.",
    noPermission: "You don't have permission to move it.",
    toolMissing: "The tool is no longer installed. It wasn't touched.",
    toolFailed: "The command ended with an error. Check its output.",
    canceled: "It stopped before reaching this item.",
    failed: "Something went wrong and it wasn't cleaned.",
  },
};

const dictionaries = { es, en };
const locales: Record<Lang, string> = { es: "es-MX", en: "en-US" };
const STORAGE_KEY = "el-buscador-lang";

export function initialLang(): Lang {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === "es" || saved === "en") return saved;
  } catch {
    // Sin almacenamiento se usa el idioma del sistema.
  }
  return navigator.language.toLowerCase().startsWith("es") ? "es" : "en";
}

export function saveLang(lang: Lang) {
  try {
    localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    // Solo se pierde recordar el idioma para la próxima vez.
  }
}

const GB = 1024 ** 3;
const MB = 1024 ** 2;
const KB = 1024;
const MONTH = 30 * 24 * 3600;
const YEAR = 365 * 24 * 3600;
const DAY = 24 * 3600;

export function makeFormat(lang: Lang) {
  const locale = locales[lang];
  const relative = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  return {
    number: (value: number) => value.toLocaleString(locale),
    size(bytes: number) {
      if (bytes >= GB) {
        const value = bytes / GB;
        return `${value.toLocaleString(locale, { maximumFractionDigits: value < 100 ? 1 : 0 })} GB`;
      }
      if (bytes >= MB) return `${Math.round(bytes / MB).toLocaleString(locale)} MB`;
      return `${Math.ceil(bytes / KB).toLocaleString(locale)} KB`;
    },
    ago(unixSeconds: number) {
      const seconds = Date.now() / 1000 - unixSeconds;
      if (seconds >= YEAR) return relative.format(-Math.floor(seconds / YEAR), "year");
      if (seconds >= MONTH) return relative.format(-Math.floor(seconds / MONTH), "month");
      return relative.format(-Math.floor(seconds / DAY), "day");
    },
  };
}

export const I18n = createContext({ lang: "es" as Lang, t: es, format: makeFormat("es"), toggle: () => {} });

export function dictionary(lang: Lang) {
  return dictionaries[lang];
}

export function useI18n() {
  return useContext(I18n);
}
