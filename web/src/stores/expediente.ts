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
   * Idempotente: chamar de novo não refaz a requisição. O expediente muda raramente
   * (só quando o staff edita), e cada tela chama isto no onMounted sem saber se
   * outra já chamou. Depois de uma edição, use recarregar().
   */
  async function carregar() {
    if (horas.value || carregando.value) return
    await recarregar()
  }

  /** Força o refetch — depois de editar horário, dias ou exceções. */
  async function recarregar() {
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

  /**
   * O restaurante abre nesta data? É uma DICA de UI — o backend é a autoridade
   * (recusa reserva em dia fechado). Existe para a agenda mostrar "Fechado"
   * explicitamente, em vez de uma grade vazia que parece "sem mesas".
   *
   * O dia da semana vem de meio-dia UTC: a data "2026-08-03" é segunda-feira
   * independentemente de fuso, e o meio-dia evita qualquer rollover de data.
   * getUTCDay devolve 0=domingo … 6=sábado, a mesma convenção do open_weekdays.
   */
  function diaAberto(dia: string): boolean {
    if (!horas.value) return true // enquanto carrega, não afirma "fechado"

    const ex = horas.value.exceptions.find((e) => e.day === dia)
    if (ex) return ex.is_open

    const weekday = new Date(`${dia}T12:00:00Z`).getUTCDay()
    return horas.value.open_weekdays.includes(weekday)
  }

  async function salvarHoras(input: {
    start: string
    end: string
    tz: string
    open_weekdays: number[]
  }): Promise<void> {
    horas.value = await api.updateServiceHours(input)
  }

  async function salvarExcecao(ex: {
    day: string
    is_open: boolean
    note: string
  }): Promise<void> {
    await api.saveException(ex)
    await recarregar()
  }

  async function removerExcecao(day: string): Promise<void> {
    await api.deleteException(day)
    await recarregar()
  }

  return {
    horas,
    carregando,
    erro,
    tz,
    aberturaMin,
    fechamentoMin,
    pronto,
    carregar,
    recarregar,
    diaAberto,
    salvarHoras,
    salvarExcecao,
    removerExcecao,
  }
})
