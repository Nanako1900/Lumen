/** @type {import('tailwindcss').Config} */
export default {
  darkMode: "class",
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        // Semantic aliases layered on Tailwind's zinc/indigo palette
        // (see web-design.md §4.4 深色调色板).
        accent: {
          DEFAULT: "#818cf8", // indigo-400
          strong: "#4f46e5", // indigo-600
          hover: "#6366f1", // indigo-500
        },
      },
      fontFamily: {
        sans: [
          "system-ui",
          "-apple-system",
          "BlinkMacSystemFont",
          "Segoe UI",
          "Roboto",
          "Helvetica Neue",
          "Arial",
          "sans-serif",
        ],
      },
      maxWidth: {
        content: "64rem", // max-w-5xl container
      },
    },
  },
  plugins: [],
};
