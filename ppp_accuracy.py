#!/usr/bin/env python3
"""
ppp_accuracy.py — анализ точности PPP-AR обработки.

Использование:
    python ppp_accuracy.py FILE [FILE ...] [--out report.txt] [--plots] [--per-station-filter]

Поддерживаемые форматы CSV:
  ФАГС: date,station,city,lat,lon,h_m,q,nsat,dN_m,dE_m,dU_m,r3d_m,status,duration_s
  IGS:  date,station,lat,lon,h_m,q,nsat,dN_m,dE_m,dU_m,r3d_m,status,duration_s
  (формат определяется автоматически по наличию столбца 'city')
"""

import sys
import argparse
import textwrap
from pathlib import Path

import numpy as np
import pandas as pd

# ── опциональный matplotlib ──────────────────────────────────────────────────
try:
    import matplotlib
    matplotlib.use("Agg")           # рисуем в файл без дисплея
    import matplotlib.pyplot as plt
    HAS_MPL = True
except ImportError:
    HAS_MPL = False

# ── пороги оценки точности (RMS 3D, мм) ─────────────────────────────────────
GRADE_THRESHOLDS = [5, 10, 20, 50]           # мм
GRADE_LABELS     = ["≤5 мм", "5–10 мм", "10–20 мм", "20–50 мм", ">50 мм"]

# ── коды «фикс» решений ─────────────────────────────────────────────────────
FIX_Q_VALUES = {1}          # Q=1 — ambiguity fixed

# ── IQR-коэффициент для отсева выбросов ─────────────────────────────────────
IQR_FACTOR = 3.0

# ─────────────────────────────────────────────────────────────────────────────
# Загрузка и предобработка
# ─────────────────────────────────────────────────────────────────────────────

def load_csv(path: str) -> pd.DataFrame:
    """Загружает один CSV-файл, автоматически определяет формат."""
    df = pd.read_csv(path, low_memory=False)
    df.columns = df.columns.str.strip()

    # Нормализуем: в ФАГС есть 'city', в IGS — нет
    if "city" not in df.columns:
        df.insert(df.columns.get_loc("station") + 1, "city", "")

    # Числовые столбцы
    for col in ("lat", "lon", "h_m", "q", "nsat",
                "dN_m", "dE_m", "dU_m", "r3d_m", "duration_s"):
        if col in df.columns:
            df[col] = pd.to_numeric(df[col], errors="coerce")

    # Метка источника
    df["_file"] = Path(path).name
    return df


def load_files(paths: list[str]) -> pd.DataFrame:
    frames = [load_csv(p) for p in paths]
    return pd.concat(frames, ignore_index=True)


def filter_ok(df: pd.DataFrame) -> pd.DataFrame:
    """Оставляем только строки status=ok, lat≠0, dN/dE/dU не NaN и не все нули
    (нули означают отсутствие эталонных координат)."""
    mask = (
        (df["status"].str.strip().str.lower() == "ok")
        & (df["lat"].abs() > 1e-6)
        & df["dN_m"].notna()
        & df["dE_m"].notna()
        & df["dU_m"].notna()
        # Отбрасываем строки где все три компоненты == 0 (нет эталона)
        & ~((df["dN_m"].abs() < 1e-9) & (df["dE_m"].abs() < 1e-9) & (df["dU_m"].abs() < 1e-9))
    )
    return df[mask].copy()


def iqr_filter_global(df: pd.DataFrame, factor: float = IQR_FACTOR) -> pd.DataFrame:
    """Глобальный IQR-фильтр по dN, dE, dU."""
    mask = pd.Series(True, index=df.index)
    for col in ("dN_m", "dE_m", "dU_m"):
        q1, q3 = df[col].quantile(0.25), df[col].quantile(0.75)
        iqr = q3 - q1
        lo, hi = q1 - factor * iqr, q3 + factor * iqr
        mask &= df[col].between(lo, hi)
    n_before = len(df)
    df = df[mask].copy()
    n_removed = n_before - len(df)
    return df, n_removed


def iqr_filter_per_station(df: pd.DataFrame, factor: float = IQR_FACTOR) -> pd.DataFrame:
    """IQR-фильтр внутри каждого пункта (для месячных данных)."""
    keep = []
    for _, grp in df.groupby("station"):
        if len(grp) < 4:
            keep.append(grp)
            continue
        mask = pd.Series(True, index=grp.index)
        for col in ("dN_m", "dE_m", "dU_m"):
            q1, q3 = grp[col].quantile(0.25), grp[col].quantile(0.75)
            iqr = q3 - q1
            if iqr < 1e-9:
                continue
            lo, hi = q1 - factor * iqr, q3 + factor * iqr
            mask &= grp[col].between(lo, hi)
        keep.append(grp[mask])
    result = pd.concat(keep)
    return result, len(df) - len(result)

# ─────────────────────────────────────────────────────────────────────────────
# Статистика
# ─────────────────────────────────────────────────────────────────────────────

def rms(arr):
    a = np.asarray(arr, dtype=float)
    return float(np.sqrt(np.mean(a**2))) if len(a) else float("nan")


def station_stats(df: pd.DataFrame) -> pd.DataFrame:
    """СКП и статистика fix-решений по каждому пункту."""
    rows = []
    for station, grp in df.groupby("station"):
        city = grp["city"].iloc[0] if grp["city"].iloc[0] else ""
        n = len(grp)
        rms_n = rms(grp["dN_m"]) * 1000      # мм
        rms_e = rms(grp["dE_m"]) * 1000
        rms_u = rms(grp["dU_m"]) * 1000
        rms_3d = rms(grp["r3d_m"]) * 1000 if "r3d_m" in grp else \
                 float(np.sqrt(rms_n**2 + rms_e**2 + rms_u**2))
        fix_pct = 100.0 * grp["q"].isin(FIX_Q_VALUES).sum() / n if n else 0.0
        rows.append({
            "station": station,
            "city":    city,
            "n":       n,
            "rms_N_mm": rms_n,
            "rms_E_mm": rms_e,
            "rms_U_mm": rms_u,
            "rms_3d_mm": rms_3d,
            "fix_pct": fix_pct,
        })
    stats = pd.DataFrame(rows).sort_values("station").reset_index(drop=True)
    return stats


def global_stats(df: pd.DataFrame, st: pd.DataFrame) -> dict:
    """Суммарные показатели: общий СКП и средний-по-пунктам СКП."""
    total_n   = len(df)
    fix_global = 100.0 * df["q"].isin(FIX_Q_VALUES).sum() / total_n if total_n else 0.0
    fix_mean  = st["fix_pct"].mean()

    # Общий СКП (все эпохи вместе)
    g_rms_n = rms(df["dN_m"]) * 1000
    g_rms_e = rms(df["dE_m"]) * 1000
    g_rms_u = rms(df["dU_m"]) * 1000
    g_rms_3d = rms(df["r3d_m"]) * 1000 if "r3d_m" in df else \
               float(np.sqrt(g_rms_n**2 + g_rms_e**2 + g_rms_u**2))

    # Средний по пунктам СКП
    m_rms_n  = st["rms_N_mm"].mean()
    m_rms_e  = st["rms_E_mm"].mean()
    m_rms_u  = st["rms_U_mm"].mean()
    m_rms_3d = st["rms_3d_mm"].mean()

    return {
        "total_n":    total_n,
        "n_stations": len(st),
        "fix_global": fix_global,
        "fix_mean":   fix_mean,
        "g_rms_n":    g_rms_n,
        "g_rms_e":    g_rms_e,
        "g_rms_u":    g_rms_u,
        "g_rms_3d":   g_rms_3d,
        "m_rms_n":    m_rms_n,
        "m_rms_e":    m_rms_e,
        "m_rms_u":    m_rms_u,
        "m_rms_3d":   m_rms_3d,
    }


def accuracy_grades(st: pd.DataFrame) -> list[tuple[str, int, float]]:
    """Разбивка пунктов по RMS 3D на классы точности."""
    vals = st["rms_3d_mm"].values
    thresholds_m = [t for t in GRADE_THRESHOLDS]   # мм
    counts = []
    prev = 0
    for thr, lbl in zip(thresholds_m, GRADE_LABELS[:-1]):
        c = int(np.sum((vals > prev) & (vals <= thr)))
        counts.append((lbl, c, 100.0 * c / len(vals) if len(vals) else 0.0))
        prev = thr
    c = int(np.sum(vals > thresholds_m[-1]))
    counts.append((GRADE_LABELS[-1], c, 100.0 * c / len(vals) if len(vals) else 0.0))
    return counts

# ─────────────────────────────────────────────────────────────────────────────
# Вывод
# ─────────────────────────────────────────────────────────────────────────────

def fmt_line(w=80):
    return "─" * w


def report_text(df_raw: pd.DataFrame, df: pd.DataFrame, n_removed: int,
                st: pd.DataFrame, gs: dict, grades: list,
                files: list[str]) -> str:
    lines = []
    add = lines.append

    add(fmt_line())
    add("  АНАЛИЗ ТОЧНОСТИ PPP-AR  |  ppp_accuracy.py")
    add(fmt_line())
    add(f"  Файлы     : {', '.join(Path(f).name for f in files)}")
    add(f"  Строк всего / ok / после фильтра выбросов: "
        f"{len(df_raw)} / {len(df_raw)} / {len(df)}  (удалено выбросов: {n_removed})")
    add(f"  Пунктов   : {gs['n_stations']}   Эпох: {gs['total_n']}")
    add("")

    # ── Fix-решения ──────────────────────────────────────────────────────────
    add("  ── ДОЛЯ FIX-РЕШЕНИЙ (Q=1) ──")
    add(f"  Глобально (все эпохи): {gs['fix_global']:6.1f}%")
    add(f"  Среднее по пунктам  : {gs['fix_mean']:6.1f}%")
    add("")

    # ── Общий СКП ────────────────────────────────────────────────────────────
    add("  ── СКП (мм)  [глобальный / средний по пунктам] ──")
    add(f"  {'':12s}  {'N':>8s}  {'E':>8s}  {'U':>8s}  {'3D':>8s}")
    add(f"  {'Глобальный':12s}  {gs['g_rms_n']:8.2f}  {gs['g_rms_e']:8.2f}"
        f"  {gs['g_rms_u']:8.2f}  {gs['g_rms_3d']:8.2f}")
    add(f"  {'Ср. по пунктам':14s}  {gs['m_rms_n']:8.2f}  {gs['m_rms_e']:8.2f}"
        f"  {gs['m_rms_u']:8.2f}  {gs['m_rms_3d']:8.2f}")
    add("")

    # ── Точность по классам ──────────────────────────────────────────────────
    add("  ── КЛАССЫ ТОЧНОСТИ (RMS 3D) ──")
    for lbl, cnt, pct in grades:
        bar = "█" * int(round(pct / 2))
        add(f"  {lbl:<10s}  {cnt:4d} пунктов  ({pct:5.1f}%)  {bar}")
    add("")

    # ── Таблица по пунктам ───────────────────────────────────────────────────
    add("  ── СКП ПО ПУНКТАМ ──")
    hdr = (f"  {'Пункт':<6s}  {'Город':<22s}  {'N':>5s}  "
           f"{'rmsN':>7s}  {'rmsE':>7s}  {'rmsU':>7s}  {'rms3D':>7s}  {'Fix%':>6s}")
    add(hdr)
    add("  " + fmt_line(len(hdr) - 2))
    for _, row in st.iterrows():
        city = str(row["city"])[:22] if row["city"] else ""
        add(f"  {row['station']:<6s}  {city:<22s}  {int(row['n']):>5d}  "
            f"{row['rms_N_mm']:>7.2f}  {row['rms_E_mm']:>7.2f}  "
            f"{row['rms_U_mm']:>7.2f}  {row['rms_3d_mm']:>7.2f}  "
            f"{row['fix_pct']:>5.1f}%")
    add(fmt_line())

    return "\n".join(lines)

# ─────────────────────────────────────────────────────────────────────────────
# Графики
# ─────────────────────────────────────────────────────────────────────────────

def make_plots(df: pd.DataFrame, st: pd.DataFrame, gs: dict, out_prefix: str):
    if not HAS_MPL:
        print("  [предупреждение] matplotlib не установлен, графики не строятся")
        return

    # 1. Гистограммы dN, dE, dU по всем эпохам
    fig, axes = plt.subplots(1, 3, figsize=(14, 4))
    fig.suptitle("Распределение остатков PPP (мм)", fontsize=13)
    for ax, col, lbl, clr in zip(
        axes,
        ["dN_m", "dE_m", "dU_m"],
        ["dN", "dE", "dU"],
        ["steelblue", "darkorange", "seagreen"]
    ):
        vals = df[col].values * 1000
        ax.hist(vals, bins=60, color=clr, edgecolor="none", alpha=0.85)
        ax.axvline(0, color="black", linewidth=0.8, linestyle="--")
        ax.set_xlabel(f"{lbl}, мм")
        ax.set_ylabel("Количество эпох")
        ax.set_title(f"СКП = {rms(df[col])*1000:.2f} мм")
    plt.tight_layout()
    p = f"{out_prefix}_hist.png"
    plt.savefig(p, dpi=150)
    plt.close()
    print(f"  График гистограмм  : {p}")

    # 2. Столбчатая диаграмма RMS 3D по пунктам (топ-50 по rms_3d)
    top = st.nlargest(min(50, len(st)), "rms_3d_mm").sort_values("rms_3d_mm", ascending=False)
    fig, ax = plt.subplots(figsize=(max(10, len(top) * 0.35), 5))
    clrs = ["#e74c3c" if v > 20 else "#f39c12" if v > 10 else "#27ae60"
            for v in top["rms_3d_mm"]]
    ax.bar(top["station"], top["rms_3d_mm"], color=clrs, edgecolor="none")
    ax.axhline(gs["m_rms_3d"], color="navy", linewidth=1.2, linestyle="--",
               label=f"Среднее {gs['m_rms_3d']:.1f} мм")
    ax.set_ylabel("RMS 3D, мм")
    ax.set_title("RMS 3D по пунктам")
    ax.set_xticklabels(top["station"], rotation=90, fontsize=7)
    ax.legend()
    plt.tight_layout()
    p = f"{out_prefix}_rms3d.png"
    plt.savefig(p, dpi=150)
    plt.close()
    print(f"  График RMS 3D      : {p}")

    # 3. Диаграмма классов точности
    grades_data = accuracy_grades(st)
    labels  = [g[0] for g in grades_data]
    counts  = [g[1] for g in grades_data]
    colors  = ["#27ae60", "#2ecc71", "#f39c12", "#e67e22", "#e74c3c"]
    fig, ax = plt.subplots(figsize=(7, 4))
    bars = ax.bar(labels, counts, color=colors[:len(labels)], edgecolor="none")
    for bar, cnt in zip(bars, counts):
        if cnt:
            ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 0.3,
                    str(cnt), ha="center", va="bottom", fontsize=9)
    ax.set_ylabel("Количество пунктов")
    ax.set_title("Распределение пунктов по классам точности (RMS 3D)")
    plt.tight_layout()
    p = f"{out_prefix}_grades.png"
    plt.savefig(p, dpi=150)
    plt.close()
    print(f"  График классов     : {p}")

    # 4. Fix% по пунктам
    st_sorted = st.sort_values("fix_pct")
    fig, ax = plt.subplots(figsize=(max(10, len(st_sorted) * 0.35), 5))
    clrs = ["#e74c3c" if v < 50 else "#f39c12" if v < 80 else "#27ae60"
            for v in st_sorted["fix_pct"]]
    ax.bar(st_sorted["station"], st_sorted["fix_pct"], color=clrs, edgecolor="none")
    ax.axhline(gs["fix_global"], color="navy", linewidth=1.2, linestyle="--",
               label=f"Глобально {gs['fix_global']:.1f}%")
    ax.set_ylabel("Fix-решений, %")
    ax.set_ylim(0, 105)
    ax.set_title("Доля Fix-решений (Q=1) по пунктам")
    ax.set_xticklabels(st_sorted["station"], rotation=90, fontsize=7)
    ax.legend()
    plt.tight_layout()
    p = f"{out_prefix}_fixrate.png"
    plt.savefig(p, dpi=150)
    plt.close()
    print(f"  График Fix%        : {p}")

# ─────────────────────────────────────────────────────────────────────────────
# main
# ─────────────────────────────────────────────────────────────────────────────

def parse_args():
    ap = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("files", nargs="+", metavar="FILE",
                    help="CSV-файлы с результатами обработки (ФАГС или IGS)")
    ap.add_argument("--out", metavar="FILE",
                    help="Сохранить текстовый отчёт в файл")
    ap.add_argument("--plots", action="store_true",
                    help="Строить графики (требует matplotlib)")
    ap.add_argument("--per-station-filter", action="store_true",
                    help="Дополнительно фильтровать выбросы внутри каждого пункта "
                         "(рекомендуется для месячных данных)")
    ap.add_argument("--plot-prefix", default="ppp_accuracy",
                    metavar="PREFIX",
                    help="Префикс имён файлов графиков (default: ppp_accuracy)")
    return ap.parse_args()


def main():
    args = parse_args()

    # Загрузка
    df_all = load_files(args.files)
    df_ok  = filter_ok(df_all)

    if len(df_ok) == 0:
        print("Нет строк с status=ok и ненулевыми координатами. Проверьте файлы.")
        sys.exit(1)

    # Глобальный IQR-фильтр
    df_clean, n_rem = iqr_filter_global(df_ok)

    # Дополнительный по-пунктный фильтр
    if args.per_station_filter:
        df_clean, n_rem2 = iqr_filter_per_station(df_clean)
        n_rem += n_rem2

    # Статистика
    st = station_stats(df_clean)
    gs = global_stats(df_clean, st)
    grades = accuracy_grades(st)

    # Отчёт
    text = report_text(df_ok, df_clean, n_rem, st, gs, grades, args.files)
    print(text)

    if args.out:
        Path(args.out).write_text(text, encoding="utf-8")
        print(f"\n  Отчёт сохранён: {args.out}")

    # Графики
    if args.plots:
        make_plots(df_clean, st, gs, args.plot_prefix)


if __name__ == "__main__":
    main()
