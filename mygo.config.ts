import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "OpenTools",
  identifier: "dev.opentools.desktop",
  version: "0.3.3",
  main: "./cmd/opentools",
  devUrl: "http://localhost:5173",
  devCommand: "npm run dev:web",
  buildCommand: "npm run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
});
