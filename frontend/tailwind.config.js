/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{vue,js,ts,jsx,tsx}",
  ],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        background: 'var(--color-background)',
        surface: 'var(--color-surface)',
        'surface-hover': 'var(--color-surface-hover)',
        border: 'var(--color-border)',
        
        foreground: 'var(--color-foreground)',
        // foreground-strong: pure white for text on saturated brand
        // surfaces. Reserved as scaffolding for the deferred white-tint
        // pass when --color-foreground shifts to a tinted off-white.
        'foreground-strong': 'var(--color-foreground-strong)',
        'foreground-muted': 'var(--color-foreground-muted)',
        
        primary: {
          DEFAULT: 'var(--color-primary)',
          foreground: 'var(--color-primary-foreground)',
          hover: 'var(--color-primary-hover)',
          600: 'var(--color-primary)', 
          500: 'var(--color-primary)',
        },
        
        secondary: {
            DEFAULT: 'var(--color-secondary)',
            foreground: 'var(--color-secondary-foreground)',
            hover: 'var(--color-secondary-hover)',
        },

        success: {
          DEFAULT: 'var(--color-success)',
          tint: 'var(--color-success-tint)',
          fg: 'var(--color-success-fg)',
          ring: 'var(--color-success-ring)',
        },
        warning: {
          DEFAULT: 'var(--color-warning)',
          tint: 'var(--color-warning-tint)',
          fg: 'var(--color-warning-fg)',
          ring: 'var(--color-warning-ring)',
        },
        danger: {
          DEFAULT: 'var(--color-danger)',
          tint: 'var(--color-danger-tint)',
          fg: 'var(--color-danger-fg)',
          ring: 'var(--color-danger-ring)',
        },
        // Legacy `error` alias for any code still using bg-error / text-error.
        error: 'var(--color-danger)',
        info: {
          DEFAULT: 'var(--color-info)',
          tint: 'var(--color-info-tint)',
          fg: 'var(--color-info-fg)',
          ring: 'var(--color-info-ring)',
        },
      },
      fontFamily: {
        sans: ['Inter', 'sans-serif'],
        mono: ['JetBrains Mono', 'monospace'],
      },
    },
  },
  plugins: [
    import('@tailwindcss/typography'),
  ],
}
