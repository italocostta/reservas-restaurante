import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api } from '@/api/client'
import { mensagemLegivel } from '@/api/errors'
import { limitesDaRegua, montarGrade } from '@/lib/grade'
import { hojeNoRestaurante } from '@/lib/tempo'
import type {
  CreateReservationInput,
  DateOnly,
  Reservation,
  TableAvailability,
  UUID,
} from '@/types/api'

import { useExpedienteStore } from './expediente'

export const useAgendaStore = defineStore('agenda', () => {
  const expediente = useExpedienteStore()

  const dia = ref<DateOnly>('')
  const disponibilidade = ref<TableAvailability[]>([])
  const reservas = ref<Reservation[]>([])
  const carregando = ref(false)
  const erroDeCarga = ref<string | null>(null)

  const linhas = computed(() =>
    montarGrade(disponibilidade.value, reservas.value, expediente.tz, dia.value),
  )

  const regua = computed(() =>
    limitesDaRegua(expediente.aberturaMin, expediente.fechamentoMin, linhas.value),
  )

  const totalCobertos = computed(() =>
    reservas.value.reduce((soma, r) => soma + r.party_size, 0),
  )

  /**
   * Carrega o dia inteiro em DUAS requisições — e nunca N.
   *
   *   /availability?date=     → cada mesa ativa e suas janelas LIVRES (o Go já fez
   *                             o sweep; ver seção 15 da spec)
   *   /reservations?date=     → quem está lá, para desenhar em cima
   *
   * As duas em PARALELO: elas não dependem uma da outra, e serializá-las dobraria
   * o tempo de troca de dia sem ganhar nada.
   *
   * Só as `confirmed`: uma reserva cancelada não ocupa mesa (a EXCLUDE do banco só
   * indexa status='confirmed'), então desenhá-la na grade seria mostrar uma
   * ocupação que não existe.
   */
  async function carregar(novoDia?: DateOnly) {
    await expediente.carregar()
    if (!expediente.pronto) {
      erroDeCarga.value = expediente.erro ?? 'Expediente indisponível.'
      return
    }

    dia.value = novoDia ?? dia.value ?? hojeNoRestaurante(expediente.tz)
    if (!dia.value) dia.value = hojeNoRestaurante(expediente.tz)

    carregando.value = true
    erroDeCarga.value = null

    try {
      const [grade, doDia] = await Promise.all([
        api.availability(dia.value),
        api.reservations.list({ date: dia.value, status: 'confirmed' }),
      ])
      disponibilidade.value = grade
      reservas.value = doDia
    } catch (e) {
      erroDeCarga.value = mensagemLegivel(e)
      disponibilidade.value = []
      reservas.value = []
    } finally {
      carregando.value = false
    }
  }

  /**
   * Cria e RECARREGA o dia inteiro. Não insere a reserva na lista local.
   *
   * Parece desperdício e não é: a reserva nova muda as janelas LIVRES de todas as
   * mesas que ela ocupa, e essas janelas foram calculadas pelo servidor. Emendar a
   * reserva na lista local sem recarregar deixaria a grade mostrando como livre um
   * horário que acabou de ser tomado — e o staff ofereceria a mesa para o próximo
   * cliente ao telefone.
   *
   * O caminho automático piora isso: quando `table_ids` vai vazio, quem escolhe a
   * mesa é o servidor. O cliente NÃO SABE qual mesa saiu até a resposta chegar.
   *
   * LANÇA em 400/409, como o store de mesas — o erro é do formulário, não da
   * página. Um 409 aqui é "a mesa foi tomada enquanto você digitava", e essa
   * mensagem precisa aparecer com o formulário ainda preenchido.
   */
  async function criar(input: CreateReservationInput): Promise<Reservation> {
    const nova = await api.reservations.create(input)
    await carregar()
    return nova
  }

  /**
   * Cancela e recarrega, pelo mesmo motivo: o cancelamento LIBERA o horário (soft
   * delete → sai do índice parcial da EXCLUDE), e a janela livre que reabriu foi
   * calculada pelo servidor.
   */
  async function cancelar(id: UUID): Promise<void> {
    await api.reservations.cancel(id)
    await carregar()
  }

  return {
    dia,
    disponibilidade,
    reservas,
    carregando,
    erroDeCarga,
    linhas,
    regua,
    totalCobertos,
    carregar,
    criar,
    cancelar,
  }
})
