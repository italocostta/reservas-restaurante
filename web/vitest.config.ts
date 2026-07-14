import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vitest/config'

export default defineConfig({
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    include: ['src/**/*.test.ts'],

    /**
     * O RUNNER RODA EM UTC, DE PROPÓSITO — e esta é a linha mais importante deste
     * arquivo.
     *
     * O tempo.ts existe para garantir que as conversões usem o fuso do RESTAURANTE
     * e nunca o do navegador. Mas a máquina de dev deste projeto ESTÁ em
     * America/Sao_Paulo — o mesmo fuso do restaurante. Nela, uma implementação
     * ingênua (`new Date('2026-08-01T20:00')`, que usa o fuso da máquina) produz
     * exatamente o mesmo resultado que a correta.
     *
     * Medido, não suposto: com o tempo.ts quebrado de propósito para usar o fuso do
     * navegador, o teste falhava em 1 de 10 casos rodando em São Paulo, e em 5 de 10
     * rodando em UTC.
     *
     * Um teste que só falha na máquina de OUTRA pessoa é pior que nenhum: ele dá
     * confiança falsa justamente a quem escreveu o código. Fixar o runner em UTC faz
     * a suíte morder onde ela precisa morder, na máquina de quem está mexendo.
     */
    env: { TZ: 'UTC' },
  },
})
