/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_UPDATES_LATEST_URL?: string;
  readonly VITE_WEB_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
