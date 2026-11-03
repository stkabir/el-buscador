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
  devNote: "Suma solo lo marcado como Seguro. Esta versión solo muestra: todavía no borra nada.",
  devProjects: "Proyectos",
  devCaches: "Cachés de herramientas",
  lastActivity: (when: string) => `Última actividad ${when}`,
  switchLang: "Switch to English",
  switchLangShort: "EN",
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
  devNote: "Only counts items marked Safe. This version only shows them: it doesn't delete anything yet.",
  devProjects: "Projects",
  devCaches: "Tool caches",
  lastActivity: (when) => `Last activity ${when}`,
  switchLang: "Cambiar a español",
  switchLangShort: "ES",
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
