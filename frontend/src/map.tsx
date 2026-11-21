import { hierarchy, treemap, type HierarchyRectangularNode } from "d3-hierarchy";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { ScanService } from "../bindings/el-buscador";
import { focusRing, secondaryButton } from "./cleanup";
import { useI18n } from "./i18n";

type View = Awaited<ReturnType<typeof ScanService.Tree>>;
type TreeNode = View["node"];

/** Un rectángulo del mapa. "Other" junta lo que es muy chico para dibujarlo aparte. */
type Cell = { name: string; path: string; size: number; isDir: boolean; other: boolean; children: Cell[] };

const GROUP_HEADER = 20;
const MIN_LABEL_WIDTH = 56;
const MIN_LABEL_HEIGHT = 18;
// En celdas chicas el redondeo las vuelve manchas.
const MIN_ROUNDED = 12;

// Pasan validate_palette.js contra el fondo oscuro, y cada tinta contrasta 5.4:1 o más
// con su relleno. El estado nunca depende solo del color: también lleva un símbolo.
const levels: Record<string, { fill: string; ink: string; mark: string }> = {
  safe: { fill: "#14a085", ink: "#0f1115", mark: "✓" },
  review: { fill: "#c2841a", ink: "#0f1115", mark: "!" },
  keep: { fill: "#c2334d", ink: "#ffffff", mark: "✕" },
  none: { fill: "#4b5563", ink: "#ffffff", mark: "" },
};

function toCell(node: TreeNode, otherLabel: string, removed: Set<string>): Cell {
  const children = (node.children ?? []).filter((child) => !removed.has(child.path)).map((child) => toCell(child, otherLabel, removed));
  if (node.other > 0) {
    children.push({ name: otherLabel, path: `${node.path}\0other`, size: node.other, isDir: false, other: true, children: [] });
  }
  return { name: node.name, path: node.path, size: node.size, isDir: node.isDir, other: false, children };
}

/** Muestra de un vistazo qué ocupa más, y lleva a la carpeta al hacer clic. */
export function DiskMap({ root, removed, onList }: { root: string; removed: Set<string>; onList: () => void }) {
  const { t, format, lang } = useI18n();
  const [path, setPath] = useState(root);
  const [view, setView] = useState<View | null>(null);
  const [failed, setFailed] = useState(false);
  const [box, setBox] = useState({ width: 0, height: 0 });
  const [hover, setHover] = useState<{ cell: Cell; x: number; y: number } | null>(null);
  const area = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let current = true;
    setFailed(false);
    ScanService.Tree(path)
      .then((next) => {
        if (current) setView(next);
      })
      .catch((err) => {
        console.error(err);
        if (current) setFailed(true);
      });
    return () => {
      current = false;
    };
  }, [path]);

  useEffect(() => {
    const element = area.current;
    if (!element) return;
    const observer = new ResizeObserver(([entry]) => {
      setBox({ width: Math.floor(entry.contentRect.width), height: Math.floor(entry.contentRect.height) });
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [view !== null]);

  const cells = useMemo(() => {
    if (!view || box.width === 0 || box.height === 0) return [];
    const data = hierarchy(toCell(view.node, t.mapOther, removed), (cell) => (cell.children.length > 0 ? cell.children : null))
      .sum((cell) => (cell.children.length > 0 ? 0 : Math.max(cell.size, 0)))
      .sort((a, b) => (b.value ?? 0) - (a.value ?? 0));
    if (!data.value) return [];
    const layout = treemap<Cell>()
      .size([box.width, box.height])
      .paddingInner(2)
      .paddingTop((node) => (node.depth === 0 ? 0 : GROUP_HEADER))
      .round(true)(data);
    return layout.descendants().filter((node) => node.depth > 0);
  }, [view, box, removed, t]);

  if (failed) return <p class="py-6 text-red-300">{t.folderOpenFailed}</p>;
  if (!view) return <p class="py-6 text-gray-400">{t.loading}</p>;

  const levelNames: Record<string, string> = { safe: t.levelSafe, review: t.levelReview, keep: t.levelKeep, none: t.levelNone };
  const levelOf = (cell: Cell) => (cell.other ? "none" : (view.advice?.[cell.path]?.level ?? "none"));

  function describe(cell: Cell) {
    return `${cell.name}, ${format.size(cell.size)}, ${levelNames[levelOf(cell)]}`;
  }

  function renderCell(node: HierarchyRectangularNode<Cell>) {
    const cell = node.data;
    const width = node.x1 - node.x0;
    const height = node.y1 - node.y0;
    if (width < 1 || height < 1) return null;
    const style = levels[levelOf(cell)];
    const isGroup = node.children !== undefined;
    const showLabel = width >= MIN_LABEL_WIDTH && height >= MIN_LABEL_HEIGHT;
    const canOpen = cell.isDir && !cell.other;
    const position = { left: node.x0, top: node.y0, width, height };
    const tip = {
      onMouseMove: (event: MouseEvent) => setHover({ cell, x: event.clientX, y: event.clientY }),
      onMouseLeave: () => setHover(null),
    };
    const label = showLabel && (
      <span class="block truncate px-1.5 text-left text-xs leading-5 font-medium">
        {style.mark && <span aria-hidden="true">{style.mark} </span>}
        {cell.name}
        {height >= 2 * MIN_LABEL_HEIGHT && !isGroup && <span class="block font-normal opacity-80">{format.size(cell.size)}</span>}
      </span>
    );

    // Un grupo solo dibuja su marco y su encabezado. Sus hijos van encima, como
    // hermanos en el DOM, porque un botón no puede contener otros botones.
    if (isGroup) {
      return (
        <div key={cell.path} class="absolute overflow-hidden rounded bg-gray-800" style={position}>
          <button
            type="button"
            aria-label={describe(cell)}
            data-map-dir={cell.path}
            onClick={() => setPath(cell.path)}
            {...tip}
            class={`block w-full cursor-pointer truncate hover:brightness-125 ${focusRing}`}
            style={{ height: GROUP_HEADER, background: style.fill, color: style.ink }}
          >
            {label}
          </button>
        </div>
      );
    }
    const common = {
      class: `absolute overflow-hidden text-left ${Math.min(width, height) >= MIN_ROUNDED ? "rounded-sm" : ""} ${canOpen ? "cursor-pointer hover:brightness-125" : "cursor-default"} ${focusRing}`,
      style: { ...position, background: style.fill, color: style.ink },
      ...tip,
    };
    if (canOpen) {
      return (
        <button key={cell.path} type="button" aria-label={describe(cell)} data-map-dir={cell.path} onClick={() => setPath(cell.path)} {...common}>
          {label}
        </button>
      );
    }
    return (
      <div key={cell.path} role="img" aria-label={describe(cell)} {...common}>
        {label}
      </div>
    );
  }

  return (
    <div class="flex h-full flex-col gap-2">
      <div class="flex flex-wrap items-center gap-3 pt-2">
        <button
          type="button"
          disabled={!view.parent}
          onClick={() => setPath(view.parent)}
          class="cursor-pointer rounded-md border border-gray-600 px-3 py-1 text-sm hover:bg-gray-800 focus-visible:outline-2 focus-visible:outline-emerald-300 disabled:cursor-not-allowed disabled:opacity-40"
        >
          {t.up}
        </button>
        <p class="min-w-0 flex-1 truncate text-sm text-gray-300" title={view.node.path}>
          {view.node.path}
        </p>
        <p class="shrink-0 text-sm text-gray-400">{format.size(view.node.size)}</p>
      </div>

      <div ref={area} class="relative min-h-40 flex-1 overflow-hidden">
        {cells.map(renderCell)}
        {cells.length === 0 && box.width > 0 && <p class="py-6 text-gray-400">{t.emptyFolder}</p>}
      </div>

      <div class="flex flex-wrap items-center gap-x-4 gap-y-1 pb-1 text-xs text-gray-300">
        {Object.entries(levels).map(([level, style]) => (
          <span key={level} class="flex items-center gap-1.5">
            <span
              class="flex h-3.5 w-3.5 items-center justify-center rounded-sm text-[10px] font-bold"
              style={{ background: style.fill, color: style.ink }}
              aria-hidden="true"
            >
              {style.mark}
            </span>
            {levelNames[level]}
          </span>
        ))}
        <span class="text-gray-500">{t.mapHint}</span>
        <button type="button" onClick={onList} class={`${secondaryButton} ml-auto !px-3 !py-1 !text-xs`}>
          {t.mapAsList}
        </button>
      </div>

      {hover && (
        <div
          class="pointer-events-none fixed z-20 max-w-sm rounded-lg border border-gray-700 bg-gray-950 px-3 py-2 text-xs shadow-lg"
          style={{ left: Math.min(hover.x + 12, window.innerWidth - 320), top: Math.min(hover.y + 12, window.innerHeight - 110) }}
        >
          <p class="font-medium break-all text-gray-100">{hover.cell.other ? hover.cell.name : hover.cell.path}</p>
          <p class="mt-0.5 text-gray-300">
            {format.size(hover.cell.size)} · {levelNames[levelOf(hover.cell)]}
          </p>
          {!hover.cell.other && view.advice?.[hover.cell.path]?.reason && <p class="mt-0.5 text-gray-400">{view.advice[hover.cell.path]?.reason[lang]}</p>}
        </div>
      )}
    </div>
  );
}
