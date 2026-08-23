import { computed, ref, watch } from "vue";

export type ThemeMode = "dark" | "light";

const THEME_KEY = "cloudmeter_theme";
const ACCENT_KEY = "cloudmeter_accent";

export type ThemeAccent = string;

const defaultAccent = "#38bdf8";
const defaultLightAccent = "#0ea5e9";

function readStored<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    if (raw == null) return fallback;
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

export const theme = ref<ThemeMode>(readStored<ThemeMode>(THEME_KEY, "dark"));
export const accent = ref<ThemeAccent>(
  readStored<ThemeAccent>(ACCENT_KEY, theme.value === "dark" ? defaultAccent : defaultLightAccent),
);

function isHex(value: string): boolean {
  return /^#[0-9a-fA-F]{6}$/.test(value);
}

function clamp(n: number): number {
  return Math.max(0, Math.min(255, Math.round(n)));
}

function hexToRgb(hex: string): [number, number, number] {
  const value = hex.replace("#", "");
  const full =
    value.length === 3
      ? value.split("").map((c) => c + c).join("")
      : value.padEnd(6, "0").slice(0, 6);
  return [parseInt(full.slice(0, 2), 16), parseInt(full.slice(2, 4), 16), parseInt(full.slice(4, 6), 16)];
}

function rgbToHex(r: number, g: number, b: number): string {
  return "#" + [r, g, b].map((v) => clamp(v).toString(16).padStart(2, "0")).join("");
}

function mix(hex: string, target: string, weight: number): string {
  const a = hexToRgb(hex);
  const b = hexToRgb(target);
  return rgbToHex(
    a[0] + (b[0] - a[0]) * weight,
    a[1] + (b[1] - a[1]) * weight,
    a[2] + (b[2] - a[2]) * weight,
  );
}

function withAlpha(hex: string, alpha: number): string {
  const [r, g, b] = hexToRgb(hex);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

export function applyTheme() {
  const root = document.documentElement;
  root.setAttribute("data-theme", theme.value);
  root.style.setProperty("--accent", accent.value);
  root.style.setProperty("--accent-dark", theme.value === "dark" ? mix(accent.value, "#ffffff", 0.12) : mix(accent.value, "#000000", 0.2));
  root.style.setProperty("--accent-soft", withAlpha(accent.value, 0.12));
  root.style.setProperty("--accent-glow", withAlpha(accent.value, 0.18));
  root.style.colorScheme = theme.value;
  try {
    localStorage.setItem(THEME_KEY, JSON.stringify(theme.value));
    localStorage.setItem(ACCENT_KEY, JSON.stringify(accent.value));
  } catch { /* Storage may be unavailable in private mode. */ }
}

export function setTheme(mode: ThemeMode) {
  if (mode !== "dark" && mode !== "light") return;
  theme.value = mode;
  if (!isHex(accent.value)) {
    accent.value = mode === "dark" ? defaultAccent : defaultLightAccent;
  }
  applyTheme();
}

export function toggleTheme() {
  setTheme(theme.value === "dark" ? "light" : "dark");
}

export function setAccentColor(value: string) {
  if (!isHex(value)) return;
  accent.value = value;
  applyTheme();
}

export const isDark = computed(() => theme.value === "dark");

// Keep the DOM in sync whenever the reactive values change.
watch([theme, accent], () => applyTheme(), { immediate: true });

export function initTheme() {
  applyTheme();
}
