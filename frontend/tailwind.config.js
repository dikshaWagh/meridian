/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  // Tone names are composed at runtime by the Badge component.
  safelist: ['badge-neutral', 'badge-ok', 'badge-warn', 'badge-bad', 'badge-accent', 'badge-ml'],
  theme: {
    extend: {
      colors: {
        ink: 'var(--text)',
        dim: 'var(--text-dim)',
        line: 'var(--border)',
        accent: 'var(--accent)',
        'accent-ink': 'var(--accent-ink)',
        'accent-soft': 'var(--accent-soft)',
        ml: 'var(--mock)',
        mock: 'var(--mock)',
        'mock-soft': 'var(--mock-soft)',
        'ok-soft': 'var(--ok-soft)',
        'warn-soft': 'var(--warn-soft)',
        'bad-soft': 'var(--bad-soft)',
        ok: 'var(--ok)',
        warn: 'var(--warn)',
        bad: 'var(--bad)',
        teal: 'var(--teal)',
        blue: 'var(--blue)',
        panel: 'var(--panel)',
        'panel-alt': 'var(--panel-alt)',
      },
      fontFamily: {
        sans: 'var(--font-sans)',
        mono: 'var(--font-mono)',
      },
      maxWidth: {
        shell: '1280px',
      },
      transitionDuration: {
        250: '250ms',
      },
    },
  },
  plugins: [],
}
