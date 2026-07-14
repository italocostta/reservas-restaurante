/// <reference types="vite/client" />

interface ImportMetaEnv {
  /**
   * Base da API em Go. Em dev, http://localhost:8080.
   *
   * Repare no que NÃO está aqui: SERVICE_START, SERVICE_END, SERVICE_TZ. O
   * expediente vem de GET /service-hours, e essa é a razão de o endpoint existir
   * (seção 15 da spec). Copiar "18:00" para cá seria a mesma verdade em dois
   * processos, com nenhum teste vigiando a divergência.
   */
  readonly VITE_API_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
