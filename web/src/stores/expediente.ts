import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api } from '@/api/client'
import { mensagemLegivel } from '@/api/errors'
import { hhmmParaMinutos } from '@/lib/tempo'
import type { ServiceHours } from '@/types/api'

/**
 * O expediente do restaurante — carregado UMA vez, do GET /service-hours.
 *
 * Este store é a razão daquele endpoint existir (seção 15 da spec). A alternativa
 * era `SERVICE_START=18:00` num .env do Vue: a mesma verdade em dois processos,
 * com nenhum teste vigiando a divergência. No dia em que o restaurante passasse a
 * abrir às 17h, o backend aceitaria a reserva das 17h e a UI não teria onde
 * desenhá-la.
 *
 * Nada mais no frontend pode conhecer 18:00, 23:00 ou America/Sao_Paulo. Se você
 * encontrar um desses literais em qualquer outro arquivo, é bug.
 */
export const useExpedienteStore = defineStore('expediente', () => {
  const horas = ref<ServiceHours | null>(null)
  const carregando = ref(false)
  const erro = ref<string | null>(null)

  /**
   * O fuso do restaurante. Enquanto não carregar, cai em UTC — e isso é
   * deliberado: um fallback para `America/Sao_Paulo` seria a MESMA cópia da
   * verdade que este store existe para eliminar, só que escondida num `??`.
   *
   * Na prática ninguém renderiza hora antes de o expediente chegar (as telas
   * esperam), então o fallback nunca aparece. Ele existe para o tipo fechar.
   */
  const tz = computed(() => horas.value?.tz ?? 'UTC')

  /** Abertura e fechamento em minutos desde a meia-noite. 18:00 → 1080. */
  const aberturaMin = computed(() =>
    horas.value ? hhmmParaMinutos(horas.value.start) : 0,
  )
  const fechamentoMin = computed(() =>
    horas.value ? hhmmParaMinutos(horas.value.end) : 0,
  )

  const pronto = computed(() => horas.value !== null)

  /**
   * Idempotente: chamar de novo não refaz a requisição. O expediente não muda
   * durante a sessão, e cada tela que precisar dele vai chamar isto no onMounted
   * sem saber se outra já chamou.
   */
  async function carregar() {
    if (horas.value || carregando.value) return

    carregando.value = true
    erro.value = null

    try {
      horas.value = await api.serviceHours()
    } catch (e) {
      erro.value = mensagemLegivel(e)
    } finally {
      carregando.value = false
    }
  }

  return { horas, carregando, erro, tz, aberturaMin, fechamentoMin, pronto, carregar }
})
