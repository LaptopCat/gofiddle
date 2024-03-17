/** @type {import('tailwindcss').Config} */
module.exports = {
    content: [
      "./*.{html,js}",
      "./assets/**/*",
      "!./assets/output.css",
      "!./node_modules",
    ],
    theme: {
      extend: {},
    },
    plugins: [],
  };