import { defineConfig } from "vitepress";

// The documentation site, built from the Markdown in docs/ and published
// to GitHub Pages at https://mikesmitty.github.io/interlock/.
export default defineConfig({
  title: "Interlock",
  description: "An LCC layout controller for model railroads: nodes by name, configuration as forms, automations like Home Assistant.",
  base: "/interlock/",
  cleanUrls: true,
  lastUpdated: true,
  srcExclude: ["**/node_modules/**", "README.md"],
  ignoreDeadLinks: [/^http:\/\/localhost/],
  themeConfig: {
    nav: [
      { text: "Guide", link: "/getting-started" },
      { text: "Reference", link: "/api" },
    ],
    sidebar: [
      {
        text: "Guide",
        items: [
          { text: "Getting started", link: "/getting-started" },
          { text: "Connections", link: "/connections" },
          { text: "Nodes and firmware", link: "/nodes" },
          { text: "Entities", link: "/entities" },
          { text: "The track plan", link: "/track-plan" },
          { text: "Train tracking", link: "/tracking" },
          { text: "Routes and interlocking", link: "/interlocking" },
          { text: "Driving trains", link: "/driving" },
          { text: "Automations", link: "/automations" },
          { text: "Trains", link: "/trains" },
          { text: "The roster", link: "/roster" },
          { text: "Decoder settings", link: "/decoders" },
          { text: "The fast clock", link: "/clock" },
          { text: "Backup and restore", link: "/backup" },
        ],
      },
      {
        text: "Reference",
        items: [
          { text: "HTTP API", link: "/api" },
          { text: "Releasing", link: "/releasing" },
          { text: "Design", link: "/design" },
          { text: "Design language", link: "/design-language" },
        ],
      },
    ],
    socialLinks: [{ icon: "github", link: "https://github.com/mikesmitty/interlock" }],
    search: { provider: "local" },
    editLink: {
      pattern: "https://github.com/mikesmitty/interlock/edit/main/docs/:path",
      text: "Edit this page on GitHub",
    },
    footer: {
      message: "GPL-3.0 licensed.",
    },
    outline: [2, 3],
  },
});
