/** @type {import('eslint').Linter.Config[]} */
export default [
  {
    ignores: [
      "build/**",
      ".docusaurus/**",
      "docs/api/**", // Generated OpenAPI docs
      "node_modules/**",
    ],
  },
];
