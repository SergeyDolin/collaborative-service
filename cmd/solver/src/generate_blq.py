#!/usr/bin/env python3
"""
Generate BLQ ocean tide loading file using pyfes (aviso-fes library).

The BLQ file is required by RTKLIB for Ocean Tide Loading (OTL) correction
when pos1-tidecorr = otl (2) is set in the configuration.

Usage:
    python3 generate_blq.py <station_name> <lon> <lat> <pyfes_config.yml> <output.blq>

Arguments:
    station_name    - RINEX MARKER NAME (station identifier)
    lon             - Longitude in degrees (East positive)
    lat             - Latitude in degrees (North positive)
    pyfes_config    - Path to pyfes YAML configuration file that references
                      ocean tide loading (radial) NetCDF model files
    output          - Output path for the BLQ file

Environment:
    DATASET_DIR     - Directory containing FES ocean loading NetCDF files
                      (interpolated by the pyfes YAML via ${DATASET_DIR})

Model data:
    Download FES2014 / FES2022 model files from:
    https://www.aviso.altimetry.fr/en/data/products/auxiliary-products/global-tide-fes.html

    Required radial (ocean loading) constituent files:
        2N2_radial.nc  K1_radial.nc   K2_radial.nc  M2_radial.nc
        N2_radial.nc   O1_radial.nc   P1_radial.nc  Q1_radial.nc
        S2_radial.nc   Mf_radial.nc   Mm_radial.nc  Ssa_radial.nc
"""

from __future__ import annotations

import argparse
import pathlib
import sys

import numpy as np


# 11 standard BLQ tidal constituents with their angular frequencies (deg/hour).
# Order follows the IERS / Scherneck BLQ table convention expected by RTKLIB.
BLQ_CONSTITUENTS: list[tuple[str, float]] = [
    ('M2',  28.9841042),
    ('S2',  30.0000000),
    ('N2',  28.4397295),
    ('K2',  30.0821373),
    ('K1',  15.0410686),
    ('O1',  13.9430356),
    ('P1',  14.9589314),
    ('Q1',  13.3986609),
    ('MF',   1.0980331),
    ('MM',   0.5443747),
    ('SSA',  0.0821373),
]


def harmonic_analysis(
    times_h: np.ndarray,
    signal: np.ndarray,
    frequencies_deg_h: list[float],
) -> tuple[list[float], list[float]]:
    """Extract tidal amplitudes and phases by least-squares harmonic analysis.

    Parameters
    ----------
    times_h:
        Time vector in hours from an arbitrary epoch.
    signal:
        Observed signal in metres.
    frequencies_deg_h:
        Angular frequencies of each tidal constituent in degrees/hour.

    Returns
    -------
    amplitudes : list[float]
        Amplitude of each constituent in metres.
    phases : list[float]
        Greenwich phase of each constituent in degrees [0, 360).
    """
    omega = np.array([f * np.pi / 180.0 for f in frequencies_deg_h])
    n = len(omega)

    # Design matrix: [1, cos(omega_1*t), sin(omega_1*t), ...]
    A = np.zeros((len(times_h), 1 + 2 * n))
    A[:, 0] = 1.0
    for i, w in enumerate(omega):
        A[:, 1 + 2 * i] = np.cos(w * times_h)
        A[:, 2 + 2 * i] = np.sin(w * times_h)

    x, _, _, _ = np.linalg.lstsq(A, signal, rcond=None)

    amplitudes: list[float] = []
    phases: list[float] = []
    for i in range(n):
        a = x[1 + 2 * i]  # cosine coefficient
        b = x[2 + 2 * i]  # sine coefficient
        amplitudes.append(float(np.sqrt(a**2 + b**2)))
        phases.append(float(np.degrees(np.arctan2(-b, a)) % 360.0))

    return amplitudes, phases


def generate_blq(
    station_name: str,
    lon: float,
    lat: float,
    config_path: str,
    output_path: str,
) -> None:
    """Compute ocean tide loading and write a BLQ file.

    The vertical (radial / U) component is derived from the pyfes FES radial
    ocean-loading model via 1-year harmonic analysis.  The horizontal
    components (West, South) require a 3-D loading Green's function not
    available through pyfes and are therefore set to zero — standard practice
    when only the vertical loading field is available.

    Parameters
    ----------
    station_name:
        RINEX MARKER NAME written into the BLQ header.  RTKLIB matches this
        name when selecting the OTL coefficients for the receiver.
    lon, lat:
        Geodetic coordinates in degrees.
    config_path:
        Path to the pyfes YAML configuration file.
    output_path:
        Destination path for the generated BLQ file.
    """
    try:
        import pyfes
    except ImportError as exc:
        raise RuntimeError(
            'pyfes is not installed.\n'
            'Build and install it from the aviso-fes repository:\n'
            '  pip install aviso-fes'
        ) from exc

    cfg = pyfes.config.load(pathlib.Path(config_path))

    if 'radial' not in cfg.models:
        raise RuntimeError(
            f"pyfes config {config_path!r} contains no 'radial' model section.\n"
            "Add a radial block that points to the *_radial.nc constituent files."
        )

    # One year of hourly timestamps — long enough for accurate analysis of all
    # 11 BLQ constituents, including the slow semi-annual (Ssa, ~0.082 deg/h).
    n_hours = 365 * 24
    start = np.datetime64('2023-01-01T00:00:00', 'us')
    times = np.array(
        [start + np.timedelta64(h, 'h') for h in range(n_hours)],
        dtype='datetime64[us]',
    )
    lons = np.full(n_hours, lon)
    lats = np.full(n_hours, lat)

    load, load_lp, flags = pyfes.evaluate_tide(
        cfg.models['radial'], times, lons, lats, settings=cfg.settings
    )

    valid = flags != 0
    if not np.any(valid):
        raise RuntimeError(
            f'No valid ocean loading data at lon={lon:.4f} lat={lat:.4f}.\n'
            'The location may be on land or outside the model domain.'
        )

    # Convert cm -> m; zero-fill invalid epochs (negligible LSQ effect)
    radial_m = (load + load_lp) / 100.0
    radial_m[~valid] = 0.0

    times_h = np.arange(n_hours, dtype=float)
    freqs = [f for _, f in BLQ_CONSTITUENTS]

    amp_u, phase_u = harmonic_analysis(times_h, radial_m, freqs)

    # Horizontal components unavailable without 3-D Green's functions -> 0
    zeros = [0.0] * len(amp_u)

    _write_blq(
        output_path, station_name, lon, lat,
        amp_u, zeros, zeros,
        phase_u, zeros, zeros,
    )


def _write_blq(
    path: str,
    station: str,
    lon: float,
    lat: float,
    amp_u: list[float],
    amp_w: list[float],
    amp_s: list[float],
    phase_u: list[float],
    phase_w: list[float],
    phase_s: list[float],
) -> None:
    """Write a BLQ file in the IERS / Scherneck format understood by RTKLIB."""
    header = ''.join(f'{name:>6s}' for name, _ in BLQ_CONSTITUENTS)

    def row(values: list[float]) -> str:
        return ' ' + ' '.join(f'{v:11.5f}' for v in values)

    with open(path, 'w') as fh:
        fh.write('$$ Ocean Tide Loading\n')
        fh.write('$$ Computed with pyfes (aviso-fes)\n')
        fh.write(f'$$ Station: {station}  Lon: {lon:.6f}  Lat: {lat:.6f}\n')
        fh.write(f' {station}\n')
        fh.write(f'$${header}\n')
        fh.write(row(amp_u) + '\n')
        fh.write(row(amp_w) + '\n')
        fh.write(row(amp_s) + '\n')
        fh.write(row(phase_u) + '\n')
        fh.write(row(phase_w) + '\n')
        fh.write(row(phase_s) + '\n')
        fh.write('$$END TABLE\n')


def main() -> None:
    parser = argparse.ArgumentParser(
        description='Generate a BLQ ocean tide loading file using pyfes.',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument('station', help='Station / RINEX MARKER NAME')
    parser.add_argument('lon',    type=float, help='Longitude in degrees (East+)')
    parser.add_argument('lat',    type=float, help='Latitude in degrees (North+)')
    parser.add_argument('config', help='Path to pyfes YAML configuration file')
    parser.add_argument('output', help='Output BLQ file path')
    args = parser.parse_args()

    generate_blq(args.station, args.lon, args.lat, args.config, args.output)
    print(f'BLQ written -> {args.output}', file=sys.stderr)


if __name__ == '__main__':
    main()
