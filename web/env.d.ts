/// <reference types="vite/client" />

declare const __PREXEL_WEB_VERSION__: string

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<object, object, unknown>
  export default component
}
