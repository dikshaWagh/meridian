/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_MODE?: 'live' | 'simulation'
  readonly VITE_MERIDIAN_URL?: string
  readonly VITE_API_BASE?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
