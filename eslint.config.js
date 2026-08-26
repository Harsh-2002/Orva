// @ts-check
import js from '@eslint/js';
import ts from 'typescript-eslint';
import astro from 'eslint-plugin-astro';
import globals from 'globals';

export default ts.config(
  {
    ignores: ['dist/**', '.astro/**', 'node_modules/**', 'build/**', 'test/**', 'frontend/**'],
  },

  js.configs.recommended,
  ...ts.configs.recommended,
  ...astro.configs.recommended,

  // Accessibility. eslint-plugin-astro declares jsx-a11y as a peer and
  // re-exports its rules against Astro templates, so this is the plugin's own
  // intended a11y setup rather than a bolt-on.
  //
  // jsx-a11y's peer range still stops at eslint ^9 while eslint-plugin-astro@3
  // requires >=10, so package.json pins its eslint peer to the root version
  // through `overrides`. That is a stale range on their side, not a real
  // incompatibility: the rules are verified to fire in this setup.
  ...astro.configs['flat/jsx-a11y-recommended'],

  // Astro's <script> blocks and the layout's inline scripts run in the browser.
  {
    files: ['**/*.astro', '**/*.ts'],
    languageOptions: {
      globals: { ...globals.browser },
    },
  },

  {
    rules: {
      // An empty catch is how the copy button silently swallowed a TypeError on
      // every non-secure origin and did nothing for the reader. Require the
      // binding to be omitted deliberately, and comment the block.
      'no-empty': ['error', { allowEmptyCatch: false }],

      // Unused code is the residue of a half-finished change. It was an unused
      // `base` in docs.astro that astro check caught last time.
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],

      eqeqeq: ['error', 'smart'],
      'no-var': 'error',
      'prefer-const': 'error',
      'no-console': ['warn', { allow: ['warn', 'error'] }],
    },
  },

  // Node scripts, last so it actually wins. Flat config is last-wins, and this
  // block sat above the general rules before, which silently re-enabled
  // no-console for it. console.log is not debug residue here: it is the entire
  // user-facing output of a command-line check.
  {
    files: ['scripts/**/*.mjs', '*.config.mjs', '*.config.js'],
    languageOptions: {
      globals: { ...globals.node },
    },
    rules: {
      'no-console': 'off',
    },
  }
);
