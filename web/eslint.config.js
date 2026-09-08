import js from '@eslint/js'
import globals from 'globals'
import tseslint from '@typescript-eslint/eslint-plugin'
import tsParser from '@typescript-eslint/parser'

export default [
  { ignores: ['dist', 'node_modules'] },
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tsParser,
      parserOptions: { ecmaVersion: 2021 },
      globals: { ...globals.browser },
    },
    plugins: {
      '@typescript-eslint': tseslint,
    },
    rules: {
      ...js.configs.recommended.rules,
      ...tseslint.configs.recommended.rules,
      '@typescript-eslint/no-explicit-any': 'warn',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
      // TypeScript handles undefined-variable checking; disable ESLint's duplicate
      'no-undef': 'off',
      // Size/complexity budgets. These stay at 'warn' on purpose: 18 findings
      // exist today, so 'error' would break every build. They are not advisory
      // either — scripts/check-ts-budget.sh counts exactly these three rules and
      // ratchets the count against scripts/ts-budget-baseline.txt, which may
      // only fall. ENFORCE-AT: 0. When the baseline reaches 0, flip all three to
      // 'error', add --max-warnings=0 to lint:eslint, and delete the soak script
      // and its baseline. The hard 500-line file cap is separate and already
      // blocking for every language via scripts/check-file-length.sh.
      'max-lines': ['warn', { max: 500, skipBlankLines: true, skipComments: true }],
      'max-lines-per-function': ['warn', { max: 80, skipBlankLines: true, skipComments: true, IIFEs: true }],
      'complexity': ['warn', 15],
    },
  },
]
