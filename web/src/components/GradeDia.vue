<script setup lang="ts">
import { computed } from 'vue'

import type { Bloco, LinhaGrade } from '@/lib/grade'
import { horaLocal, minutosParaHHMM } from '@/lib/tempo'
import type { Reservation, TableAvailability } from '@/types/api'

const props = defineProps<{
  linhas: LinhaGrade[]
  regua: { inicio: number; fim: number }
  fechamentoMin: number
  tz: string
}>()

const emit = defineEmits<{
  /** Clique num vão livre: abre o formulário com a mesa e a hora já preenchidas. */
  novaEm: [mesa: TableAvailability, minutos: number]
  abrirReserva: [reserva: Reservation]
}>()

const duracao = computed(() => props.regua.fim - props.regua.inicio)

/** Minutos do dia → posição percentual na régua. É toda a matemática do desenho. */
function pct(minutos: number): number {
  return ((minutos - props.regua.inicio) / duracao.value) * 100
}

/** As marcas de hora cheia. */
const marcas = computed(() => {
  const primeira = Math.ceil(props.regua.inicio / 60) * 60
  const ms: number[] = []
  for (let m = primeira; m <= props.regua.fim; m += 60) ms.push(m)
  return ms
})

/**
 * Onde ancorar o RÓTULO da hora em relação à sua linha.
 *
 * Centralizar todos (`-translate-x-1/2`) parece o óbvio e está errado nas pontas: a
 * primeira marca fica em 0% e a última em 100%, então metade de cada rótulo cai
 * FORA da trilha. Os do meio ficam certos, e é isso que torna o defeito traiçoeiro
 * — a grade inteira parece torta quando na verdade só as duas pontas estão.
 *
 * Primeira encosta à esquerda, última à direita, o resto centraliza.
 */
function ancora(m: number): string {
  if (m === marcas.value[0]) return 'left-0'
  if (m === marcas.value[marcas.value.length - 1]) return 'right-0'
  return 'left-1/2 -translate-x-1/2'
}

/**
 * Onde o staff clicou, em minutos, arredondado para o slot de 30.
 *
 * O arredondamento não é cosmético: sem ele, o clique viraria "19:37", e o staff
 * teria que corrigir a hora TODA vez — transformando um atalho num estorvo.
 * Restaurante marca de meia em meia hora.
 */
function minutosDoClique(evento: MouseEvent): number {
  const trilha = evento.currentTarget as HTMLElement
  const { left, width } = trilha.getBoundingClientRect()

  const fracao = (evento.clientX - left) / width
  const bruto = props.regua.inicio + fracao * duracao.value

  return Math.round(bruto / 30) * 30
}

function clicarNaTrilha(evento: MouseEvent, mesa: TableAvailability) {
  emit('novaEm', mesa, minutosDoClique(evento))
}

function rotuloDoBloco(b: Bloco): string {
  const de = horaLocal(b.reserva.starts_at, props.tz)
  const ate = horaLocal(b.reserva.ends_at, props.tz)
  return `${b.reserva.customer_name} · ${b.reserva.party_size}p · ${de}–${ate}`
}
</script>

<template>
  <div class="border-ink-800 border">
    <!-- RÉGUA DE HORAS. Fica no topo e é o eixo de tudo que vem abaixo. -->
    <div class="border-ink-800 bg-ink-900 flex border-b">
      <div class="border-ink-800 w-40 shrink-0 border-r px-3 py-2">
        <span class="rotulo">Mesa</span>
      </div>

      <div class="relative h-9 flex-1">
        <div
          v-for="m in marcas"
          :key="m"
          class="absolute top-0 bottom-0"
          :style="{ left: `${pct(m)}%` }"
        >
          <!-- O tique fica EM CIMA da linha da hora, na mesma coordenada das guias
               verticais das trilhas abaixo. É ele que prova o alinhamento a olho:
               o rótulo pode se deslocar nas pontas, o tique nunca. -->
          <span class="bg-ink-600 absolute bottom-0 h-1.5 w-px" aria-hidden="true"></span>

          <span
            class="dado text-ink-500 absolute bottom-2.5 text-[0.6875rem] whitespace-nowrap"
            :class="ancora(m)"
          >
            {{ minutosParaHHMM(m % 1440) }}
          </span>
        </div>
      </div>
    </div>

    <!-- LINHAS. Uma por mesa ativa. -->
    <div
      v-for="linha in linhas"
      :key="linha.mesa.table_id"
      class="border-ink-800 hover:bg-ink-900/40 group flex border-b last:border-b-0 transition-colors"
    >
      <!-- Etiqueta da mesa. Capacidade em mono, grande: é o número que o staff
           compara com o tamanho do grupo ao telefone. -->
      <div
        class="border-ink-800 flex w-40 shrink-0 items-center justify-between gap-2 border-r px-3 py-2.5"
      >
        <span class="font-display text-ink-100 truncate text-sm font-semibold">
          {{ linha.mesa.table_name }}
        </span>
        <span class="dado text-ink-300 shrink-0 text-base font-medium">
          {{ linha.mesa.capacity }}
        </span>
      </div>

      <!-- TRILHA. O fundo é "livre"; as reservas são desenhadas em cima.
           Clicar no fundo cria; clicar num bloco abre. -->
      <div
        class="relative h-12 flex-1 cursor-crosshair"
        role="button"
        tabindex="-1"
        :aria-label="`Nova reserva na ${linha.mesa.table_name}`"
        @click="clicarNaTrilha($event, linha.mesa)"
      >
        <!-- Depois do fechamento: hachura. A régua ESTICA para caber a última
             reserva do dia (que pode passar das 23h — validação 8 da spec), então
             precisa ficar claro onde o expediente acaba. Não é área proibida: é
             área fora do expediente, e o backend aceita um `ends_at` ali. -->
        <div
          v-if="pct(fechamentoMin) < 100"
          class="bg-ink-950/60 absolute inset-y-0 right-0"
          :style="{ left: `${pct(fechamentoMin)}%` }"
        >
          <div class="h-full w-full opacity-[0.06] [background:repeating-linear-gradient(45deg,transparent,transparent_4px,white_4px,white_5px)]"></div>
        </div>

        <!-- Linhas de hora: guias verticais fracas, para o olho seguir a coluna. -->
        <div
          v-for="m in marcas"
          :key="m"
          class="bg-ink-800/70 absolute inset-y-0 w-px"
          :style="{ left: `${pct(m)}%` }"
        ></div>

        <!-- RESERVAS. Bloco sólido de brasa: elas são o dado, tudo o mais é
             cenário. Uma COMBINAÇÃO recebe um talho listrado à esquerda — o mesmo
             grupo aparece em duas linhas, e o staff precisa ver que as mesas estão
             presas uma à outra. -->
        <button
          v-for="b in linha.blocos"
          :key="b.reserva.id"
          type="button"
          class="bg-ember-600/85 hover:bg-ember-500 border-ember-400/50 focus-visible:ring-ink-100 absolute inset-y-1 flex items-center overflow-hidden rounded-[1px] border-l-2 px-2 text-left transition-colors focus-visible:ring-2 focus-visible:outline-none"
          :style="{
            left: `${pct(b.inicioMin)}%`,
            width: `${pct(b.fimMin) - pct(b.inicioMin)}%`,
          }"
          :class="b.combinada ? 'border-l-ink-100' : 'border-l-ember-300'"
          :title="rotuloDoBloco(b)"
          @click.stop="emit('abrirReserva', b.reserva)"
        >
          <span
            class="text-ink-950 truncate text-xs font-semibold"
            :class="{ 'font-bold': b.combinada }"
          >
            <span v-if="b.combinada" class="mr-1" aria-label="combinação">⛓</span>
            {{ b.reserva.customer_name }}
          </span>
          <span class="dado text-ink-950/70 ml-1.5 shrink-0 text-[0.625rem]">
            {{ b.reserva.party_size }}p
          </span>
        </button>
      </div>
    </div>

    <p v-if="linhas.length === 0" class="text-ink-500 px-4 py-10 text-center text-sm">
      Nenhuma mesa ativa. Cadastre uma no <strong class="text-ink-300">Salão</strong>.
    </p>
  </div>
</template>
