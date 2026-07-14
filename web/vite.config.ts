import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// Sem proxy para a API. O backend em Go já libera CORS para http://localhost:5173
// (CORS_ALLOWED_ORIGIN, seção 7 da spec), então o browser fala direto com ele.
//
// Um proxy do Vite esconderia exatamente o que vale exercitar aqui: se o CORS
// quebrar, é melhor descobrir no dev — onde o proxy não existe em produção e não
// haveria ninguém para mascarar o erro.
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    // strictPort: se a 5173 estiver ocupada, FALHE. O default do Vite é cair na
    // 5174 — porta que o CORS do backend não conhece, produzindo um erro de
    // origem bem menos óbvio de diagnosticar do que "porta ocupada".
    port: 5173,
    strictPort: true,
  },
})
