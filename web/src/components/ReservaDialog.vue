<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'

import { mensagemLegivel } from '@/api/errors'
import { livreEm, maiorCapacidade, sugerirCombinacao } from '@/lib/grade'
import { formatarTelefone, soDigitos } from '@/lib/telefone'
import { hhmmParaMinutos, instanteDe, minutosParaHHMM } from '@/lib/tempo'
import { useAgendaStore } from '@/stores/agenda'
import type { DateOnly, TableAvailability, UUID } from '@/types/api'

const props = defineProps<{
  aberto: boolean
  dia: DateOnly
  tz: string
  disponibilidade: TableAvailability[]
  /** Pré-seleção vinda do clique na grade. */
  mesaPre: TableAvailability | null
  minutosPre: number | null
}>()

const emit = defineEmits<{ fechar: []; salvo: [] }>()

const agenda = useAgendaStore()

const dialogo = ref<HTMLDialogElement>()
const campoNome = ref<HTMLInputElement>()

const nome = ref('')
// Guarda só os dígitos. A máscara é aplicada na exibição (telefoneMascarado).
const telefone = ref('')
const pessoas = ref<number | null>(null)
const inicio = ref('19:00')
// Vazio = "o sistema decide a saída" (início + 2h). NÃO é mais obrigatório.
const fim = ref('')
const selecionadas = ref<UUID[]>([])
const erro = ref<string | null>(null)
const salvando = ref(false)

// A ponte entre o input mascarado e o dado limpo: o v-model do campo lê/escreve
// aqui, mas `telefone` por baixo é sempre dígito puro.
const telefoneMascarado = computed({
  get: () => formatarTelefone(telefone.value),
  set: (v: string) => {
    telefone.value = soDigitos(v)
  },
})

const DURACAO_PADRAO_MIN = 120 // 2h: default quando o staff não informa a saída.

/**
 * Os TRÊS significados de `table_ids` (seção 14 da spec) numa interface só:
 *
 *   automático + nenhuma marcada  → table_ids omitido → o servidor escolhe
 *   manual + 1 marcada            → override manual
 *   manual + 2 ou mais            → COMBINAÇÃO
 *
 * O modo é explícito e não inferido do "nenhuma mesa marcada". Sem ele, o staff
 * que abrisse o modo manual, desmarcasse tudo e enviasse cairia SILENCIOSAMENTE no
 * automático — pedindo ao sistema para escolher justo quando ele acabou de dizer
 * que queria escolher.
 */
const modo = ref<'auto' | 'manual'>('auto')

const instanteInicio = computed(() =>
  props.dia ? instanteDe(props.dia, inicio.value, props.tz) : '',
)

/**
 * A saída EFETIVA em minutos do dia. Uma fonte só para os dois casos:
 *   - "Sai" preenchido  → o que o staff digitou
 *   - "Sai" vazio       → início + 2h (default de #4)
 *
 * Pode passar de 1440 (23:00 + 2h = 25:00). Não normalizo com % aqui de propósito:
 * o >= 1440 é o próprio sinal de "vira o dia", lido logo abaixo.
 */
const fimMinTotal = computed(() =>
  fim.value
    ? hhmmParaMinutos(fim.value) + (fim.value <= inicio.value ? 1440 : 0)
    : hhmmParaMinutos(inicio.value) + DURACAO_PADRAO_MIN,
)

/** A saída como "HH:MM", já normalizada — é o que a UI mostra e o que vira instante. */
const fimHHMM = computed(() => minutosParaHHMM(fimMinTotal.value % 1440))

/**
 * A reserva atravessa a meia-noite? (22:30 → 00:30 é a última mesa do dia,
 * permitida pela validação 8.) Vale tanto para "Sai" digitado antes do início
 * quanto para o default de +2h que estoura as 24h.
 */
const viraODia = computed(() => fimMinTotal.value >= 1440)

const instanteFimReal = computed(() => {
  if (!props.dia) return ''
  if (!viraODia.value) return instanteDe(props.dia, fimHHMM.value, props.tz)

  const amanha = new Date(`${props.dia}T12:00:00Z`)
  amanha.setUTCDate(amanha.getUTCDate() + 1)
  return instanteDe(amanha.toISOString().slice(0, 10) as DateOnly, fimHHMM.value, props.tz)
})

/** A grade já traz as janelas livres calculadas pelo Go: aqui é só contenção. */
function estaLivre(mesa: TableAvailability): boolean {
  if (!instanteInicio.value || !instanteFimReal.value) return true
  return livreEm(mesa, instanteInicio.value, instanteFimReal.value)
}

const lugaresSelecionados = computed(() =>
  props.disponibilidade
    .filter((m) => selecionadas.value.includes(m.table_id))
    .reduce((soma, m) => soma + m.capacity, 0),
)

const combinando = computed(() => selecionadas.value.length > 1)

/** O teto do modo automático. A lógica e o porquê vivem em lib/grade.ts. */
const maiorMesa = computed(() => maiorCapacidade(props.disponibilidade))

/** Grupo maior que qualquer mesa sozinha: impossível no automático, sempre. */
const grandeDemaisParaAuto = computed(
  () => pessoas.value !== null && maiorMesa.value > 0 && pessoas.value > maiorMesa.value,
)

/** No automático, um grupo maior que qualquer mesa é 409 garantido. Bloqueia. */
const bloqueiaAuto = computed(() => modo.value === 'auto' && grandeDemaisParaAuto.value)

/**
 * A soma das capacidades cobre o grupo?
 *
 * É EXIBIDA, e não bloqueia o envio. Quem valida é o servidor — a regra "capacidade
 * de uma combinação = soma das capacidades" é dele (débito 13 da spec), e replicá-la
 * aqui criaria duas fontes da verdade que divergem no dia em que uma mudar.
 *
 * A UI mostra o número porque o HUMANO precisa dele para decidir; ela não julga.
 */
const cabe = computed(
  () => pessoas.value === null || lugaresSelecionados.value >= pessoas.value,
)

function alternar(id: UUID) {
  const i = selecionadas.value.indexOf(id)
  if (i === -1) selecionadas.value.push(id)
  else selecionadas.value.splice(i, 1)
}

/** Empurra para o manual com as maiores mesas já marcadas. Lógica em lib/grade.ts. */
function irParaManualComSugestao() {
  modo.value = 'manual'
  selecionadas.value = sugerirCombinacao(props.disponibilidade, pessoas.value ?? 0)
}

watch(
  () => props.aberto,
  async (aberto) => {
    if (!aberto) {
      dialogo.value?.close()
      return
    }

    nome.value = ''
    telefone.value = ''
    pessoas.value = null
    inicio.value = '19:00'
    // Volta a vazio: cada abertura recomeça com a saída no default (#4).
    fim.value = ''
    erro.value = null

    // Veio de um clique na grade: a mesa e a hora já vêm preenchidas, e o modo já
    // entra em manual — quem clicou NAQUELA mesa escolheu aquela mesa.
    if (props.mesaPre) {
      modo.value = 'manual'
      selecionadas.value = [props.mesaPre.table_id]
    } else {
      modo.value = 'auto'
      selecionadas.value = []
    }

    // A hora do clique preenche só o INÍCIO. A saída fica no default de +2h — o
    // clique diz "reservar aqui", não "reservar por exatamente duas horas".
    if (props.minutosPre !== null) {
      inicio.value = minutosParaHHMM(props.minutosPre % 1440)
    }

    dialogo.value?.showModal()
    await nextTick()
    campoNome.value?.focus()
  },
)

async function salvar() {
  const nomeLimpo = nome.value.trim()

  if (!nomeLimpo) {
    erro.value = 'Nome do cliente é obrigatório.'
    campoNome.value?.focus()
    return
  }
  if (pessoas.value === null || pessoas.value < 1) {
    erro.value = 'Quantas pessoas?'
    return
  }
  // Modo manual sem mesa marcada é contradição, não pedido: enviar isso cairia no
  // automático em silêncio.
  if (modo.value === 'manual' && selecionadas.value.length === 0) {
    erro.value = 'Marque ao menos uma mesa, ou volte para o modo automático.'
    return
  }
  // Grupo maior que qualquer mesa, no automático, é 409 garantido — e a mensagem
  // do servidor ("nenhuma mesa disponível para o horário") mentiria, porque o
  // horário não é o problema. Barramos antes, com a verdade e uma saída.
  if (bloqueiaAuto.value) {
    erro.value = `Nenhuma mesa comporta ${pessoas.value} pessoas sozinha (a maior tem ${maiorMesa.value}). Use "Eu escolho" para combinar mesas.`
    return
  }

  salvando.value = true
  erro.value = null

  try {
    await agenda.criar({
      // Omitido no automático: `undefined` some do JSON.stringify, e o Go lê um
      // slice nil — que é exatamente "escolha a mesa por mim".
      table_ids: modo.value === 'manual' ? [...selecionadas.value] : undefined,
      customer_name: nomeLimpo,
      customer_phone: telefone.value.trim(),
      party_size: pessoas.value,
      starts_at: instanteInicio.value,
      ends_at: instanteFimReal.value,
    })

    emit('salvo')
    emit('fechar')
  } catch (e) {
    // Um 409 aqui é "a mesa foi tomada enquanto você digitava" — e o formulário
    // continua aberto, preenchido, com a grade recarregada por baixo.
    erro.value = mensagemLegivel(e)
  } finally {
    salvando.value = false
  }
}
</script>

<template>
  <dialog
    ref="dialogo"
    class="bg-ink-900 border-ink-700 text-ink-100 m-auto w-[34rem] max-w-[calc(100vw-2rem)] border p-0 shadow-2xl backdrop:bg-black/70 backdrop:backdrop-blur-[2px]"
    @close="emit('fechar')"
    @cancel="emit('fechar')"
  >
    <form method="dialog" @submit.prevent="salvar">
      <div class="bg-ember-500 h-1"></div>

      <header class="border-ink-700 border-b px-6 pt-5 pb-4">
        <h2 class="font-display text-lg font-bold tracking-wide uppercase">Nova reserva</h2>
      </header>

      <div class="max-h-[65vh] space-y-5 overflow-y-auto px-6 py-6">
        <div class="grid grid-cols-[1fr_10rem] gap-4">
          <div>
            <label for="cliente" class="rotulo mb-2 block">Cliente</label>
            <input
              id="cliente"
              ref="campoNome"
              v-model="nome"
              type="text"
              autocomplete="off"
              placeholder="Maria Silva"
              class="bg-ink-950 border-ink-700 placeholder:text-ink-600 focus:border-ember-500 w-full border px-3 py-2.5 text-sm outline-none"
            />
          </div>
          <div>
            <label for="fone" class="rotulo mb-2 block">Telefone</label>
            <input
              id="fone"
              v-model="telefoneMascarado"
              type="tel"
              inputmode="numeric"
              autocomplete="off"
              placeholder="(83) 9 9999-9999"
              class="dado bg-ink-950 border-ink-700 placeholder:text-ink-600 focus:border-ember-500 w-full border px-3 py-2.5 text-sm outline-none"
            />
          </div>
        </div>

        <div class="grid grid-cols-[6rem_1fr_1fr] gap-4">
          <div>
            <label for="pessoas" class="rotulo mb-2 block">Pessoas</label>
            <input
              id="pessoas"
              v-model.number="pessoas"
              type="number"
              min="1"
              placeholder="4"
              class="dado bg-ink-950 border-ink-700 placeholder:text-ink-600 focus:border-ember-500 w-full border px-3 py-2.5 text-lg font-medium outline-none"
            />
          </div>
          <div>
            <label for="de" class="rotulo mb-2 block">Entra</label>
            <input
              id="de"
              v-model="inicio"
              type="time"
              step="1800"
              class="dado bg-ink-950 border-ink-700 focus:border-ember-500 w-full border px-3 py-2.5 text-lg font-medium outline-none"
            />
          </div>
          <div>
            <label for="ate" class="rotulo mb-2 block">
              Sai <span class="text-ink-600 normal-case">· opcional</span>
            </label>
            <input
              id="ate"
              v-model="fim"
              type="time"
              step="1800"
              class="dado bg-ink-950 border-ink-700 focus:border-ember-500 w-full border px-3 py-2.5 text-lg font-medium outline-none"
            />
            <!-- Vazio: mostra o default que será enviado, para o staff não achar que
                 esqueceu um campo obrigatório. Preenchido e virando o dia: o aviso
                 da última mesa da noite. -->
            <p v-if="!fim" class="text-ink-500 mt-1.5 text-[0.6875rem]">
              Sem preencher: sai {{ fimHHMM }} (2h).
            </p>
            <p v-else-if="viraODia" class="text-ember-300 mt-1.5 text-[0.6875rem]">
              Vira o dia — última mesa da noite.
            </p>
          </div>
        </div>

        <!-- MODO. Explícito, nunca inferido. -->
        <div>
          <span class="rotulo mb-2 block">Mesa</span>
          <div class="border-ink-700 flex border">
            <button
              type="button"
              class="font-display flex-1 px-4 py-2.5 text-xs font-bold tracking-wide uppercase transition-colors"
              :class="
                modo === 'auto'
                  ? 'bg-ember-500 text-ink-950'
                  : 'text-ink-400 hover:text-ink-100'
              "
              @click="((modo = 'auto'), (selecionadas = []))"
            >
              O sistema escolhe
            </button>
            <button
              type="button"
              class="font-display border-ink-700 flex-1 border-l px-4 py-2.5 text-xs font-bold tracking-wide uppercase transition-colors"
              :class="
                modo === 'manual'
                  ? 'bg-ember-500 text-ink-950'
                  : 'text-ink-400 hover:text-ink-100'
              "
              @click="modo = 'manual'"
            >
              Eu escolho
            </button>
          </div>

          <!-- Aviso PROATIVO: um grupo maior que qualquer mesa nunca cabe no
               automático, e o staff não deve descobrir isso só depois de apertar
               Reservar e levar um 409. O aviso aparece assim que a contagem
               ultrapassa a maior mesa, com um atalho que já marca as maiores
               mesas no manual. -->
          <div
            v-if="modo === 'auto' && grandeDemaisParaAuto"
            class="border-ember-500/50 bg-ember-500/10 mt-2 border-l-2 px-3 py-2.5"
          >
            <p class="text-ember-300 text-sm">
              Grupo de <strong>{{ pessoas }}</strong> não cabe em nenhuma mesa sozinha —
              a maior tem {{ maiorMesa }} lugares.
            </p>
            <button
              type="button"
              class="font-display text-ember-400 hover:text-ember-300 mt-1.5 text-xs font-bold tracking-wide uppercase transition-colors"
              @click="irParaManualComSugestao"
            >
              Combinar mesas →
            </button>
          </div>

          <p v-else-if="modo === 'auto'" class="text-ink-500 mt-2 text-xs">
            O sistema pega a menor mesa livre que comporte o grupo. Se nenhuma
            comportar sozinha, ele recusa —
            <strong class="text-ink-400">combinar é decisão sua</strong>.
          </p>
        </div>

        <!-- SELEÇÃO DE MESAS. Marcar duas ou mais É a combinação. -->
        <div v-if="modo === 'manual'" class="space-y-2">
          <div class="border-ink-800 max-h-52 divide-y divide-ink-800 overflow-y-auto border">
            <label
              v-for="m in disponibilidade"
              :key="m.table_id"
              class="hover:bg-ink-850 flex cursor-pointer items-center gap-3 px-3 py-2 transition-colors"
              :class="{ 'opacity-40': !estaLivre(m) }"
            >
              <input
                type="checkbox"
                :checked="selecionadas.includes(m.table_id)"
                class="accent-ember-500 h-4 w-4"
                @change="alternar(m.table_id)"
              />
              <span class="font-display flex-1 text-sm font-semibold">
                {{ m.table_name }}
              </span>
              <span class="dado text-ink-300 text-sm">{{ m.capacity }}</span>
              <!-- "Ocupada" não DESABILITA a marcação: o servidor é quem recusa,
                   com 409, e a mensagem dele é melhor do que qualquer coisa que eu
                   escreveria aqui. Desabilitar seria a UI adivinhando a regra. -->
              <span
                class="rotulo w-16 text-right"
                :class="estaLivre(m) ? 'text-sage-500' : 'text-blood-300'"
              >
                {{ estaLivre(m) ? 'Livre' : 'Ocupada' }}
              </span>
            </label>
          </div>

          <!-- A SOMA. É a única coisa que o staff precisa ver enquanto empurra as
               mesas — e ela é um guarda-corpo, não a verdade (débito 13 da spec):
               duas mesas de 4 encostadas às vezes sentam 8, às vezes 6. Quem sabe
               é quem está olhando o salão. -->
          <div
            v-if="selecionadas.length > 0"
            class="flex items-center justify-between px-1 py-1 text-sm"
          >
            <span class="text-ink-400">
              <template v-if="combinando">
                <span class="text-ink-100 font-semibold">Combinação</span>
                de {{ selecionadas.length }} mesas
              </template>
              <template v-else>Uma mesa</template>
            </span>

            <span class="dado" :class="cabe ? 'text-sage-500' : 'text-blood-300'">
              {{ lugaresSelecionados }} lugares
              <span v-if="pessoas" class="text-ink-500">/ {{ pessoas }} pessoas</span>
            </span>
          </div>

          <p v-if="combinando" class="text-ink-500 border-ink-700 border-l-2 pl-3 text-xs">
            O sistema <strong class="text-ink-400">não verifica se as mesas encostam</strong>.
            Ele registra a junção que você fez no salão.
          </p>
        </div>

        <p
          v-if="erro"
          class="border-blood-500/40 bg-blood-500/10 text-blood-300 border-l-2 px-3 py-2 text-sm"
          role="alert"
        >
          {{ erro }}
        </p>
      </div>

      <footer
        class="border-ink-700 bg-ink-850 flex items-center justify-end gap-2 border-t px-6 py-4"
      >
        <button
          type="button"
          class="font-display text-ink-400 hover:text-ink-100 px-4 py-2 text-sm font-semibold tracking-wide uppercase transition-colors"
          @click="emit('fechar')"
        >
          Cancelar
        </button>
        <button
          type="submit"
          :disabled="salvando || bloqueiaAuto"
          class="font-display bg-ember-500 text-ink-950 hover:bg-ember-400 px-5 py-2 text-sm font-bold tracking-wide uppercase transition-colors disabled:cursor-not-allowed disabled:opacity-50"
        >
          {{ salvando ? 'Reservando…' : 'Reservar' }}
        </button>
      </footer>
    </form>
  </dialog>
</template>
