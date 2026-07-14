<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import GradeDia from '@/components/GradeDia.vue'
import ListaReservas from '@/components/ListaReservas.vue'
import ReservaDetalhe from '@/components/ReservaDetalhe.vue'
import ReservaDialog from '@/components/ReservaDialog.vue'
import { diaPorExtenso, hojeNoRestaurante } from '@/lib/tempo'
import { useAgendaStore } from '@/stores/agenda'
import { useExpedienteStore } from '@/stores/expediente'
import type { Reservation, TableAvailability } from '@/types/api'

const agenda = useAgendaStore()
const expediente = useExpedienteStore()

const dialogoAberto = ref(false)
const mesaPre = ref<TableAvailability | null>(null)
const minutosPre = ref<number | null>(null)
const reservaPre = ref<Reservation | null>(null)

const reservaAberta = ref<Reservation | null>(null)

onMounted(() => agenda.carregar())

const ehHoje = computed(
  () => expediente.pronto && agenda.dia === hojeNoRestaurante(expediente.tz),
)

/** Anda N dias. A aritmética é em UTC ao meio-dia — longe de qualquer fronteira. */
function andar(dias: number) {
  const d = new Date(`${agenda.dia}T12:00:00Z`)
  d.setUTCDate(d.getUTCDate() + dias)
  agenda.carregar(d.toISOString().slice(0, 10))
}

function abrirNova(mesa: TableAvailability | null, minutos: number | null) {
  mesaPre.value = mesa
  minutosPre.value = minutos
  reservaPre.value = null
  dialogoAberto.value = true
}

/** Fecha o detalhe e abre o dialog em modo edição, pré-preenchido com a reserva. */
function abrirEdicao(reserva: Reservation) {
  reservaAberta.value = null
  mesaPre.value = null
  minutosPre.value = null
  reservaPre.value = reserva
  dialogoAberto.value = true
}
</script>

<template>
  <div>
    <!-- CABEÇALHO: o dia, os dois números que descrevem a noite, e a ação. -->
    <div class="mb-8 flex flex-wrap items-end justify-between gap-6">
      <div class="flex items-end gap-8">
        <div>
          <p class="rotulo mb-1">Serviço</p>
          <div class="flex items-center gap-3">
            <button
              type="button"
              class="text-ink-500 hover:text-ember-400 font-display text-xl leading-none transition-colors"
              aria-label="Dia anterior"
              @click="andar(-1)"
            >
              ‹
            </button>

            <span class="font-display text-ink-100 min-w-40 text-center text-2xl font-bold">
              {{ agenda.dia ? diaPorExtenso(agenda.dia, expediente.tz) : '—' }}
            </span>

            <button
              type="button"
              class="text-ink-500 hover:text-ember-400 font-display text-xl leading-none transition-colors"
              aria-label="Próximo dia"
              @click="andar(1)"
            >
              ›
            </button>

            <button
              v-if="!ehHoje && expediente.pronto"
              type="button"
              class="rotulo text-ember-400 hover:text-ember-300 ml-1 transition-colors"
              @click="agenda.carregar(hojeNoRestaurante(expediente.tz))"
            >
              Hoje
            </button>
          </div>
        </div>

        <div class="bg-ink-800 h-10 w-px"></div>

        <div>
          <p class="rotulo mb-1">Reservas</p>
          <p class="dado text-ink-100 text-4xl leading-none font-medium">
            {{ agenda.reservas.length }}
          </p>
        </div>

        <div class="bg-ink-800 h-10 w-px"></div>

        <div>
          <p class="rotulo mb-1">Cobertos</p>
          <p class="dado text-ember-400 text-4xl leading-none font-medium">
            {{ agenda.totalCobertos }}
          </p>
          <p class="text-ink-500 mt-1 text-[0.6875rem]">pessoas esperadas</p>
        </div>
      </div>

      <button
        type="button"
        class="font-display bg-ember-500 text-ink-950 hover:bg-ember-400 px-5 py-2.5 text-sm font-bold tracking-wide uppercase transition-colors"
        @click="abrirNova(null, null)"
      >
        + Nova reserva
      </button>
    </div>

    <div v-if="agenda.carregando" class="bg-ink-800 h-0.5 overflow-hidden">
      <div class="bg-ember-500 barra-carga h-full w-1/3"></div>
    </div>

    <p
      v-else-if="agenda.erroDeCarga"
      class="border-blood-500/40 bg-blood-500/10 text-blood-300 border-l-2 px-4 py-3 text-sm"
      role="alert"
    >
      {{ agenda.erroDeCarga }}
    </p>

    <template v-else>
      <GradeDia
        :linhas="agenda.linhas"
        :regua="agenda.regua"
        :fechamento-min="expediente.fechamentoMin"
        :tz="expediente.tz"
        @nova-em="abrirNova"
        @abrir-reserva="reservaAberta = $event"
      />

      <p class="text-ink-600 mt-3 text-xs">
        Clique num vão livre para reservar ali. Expediente:
        <span class="dado text-ink-400">
          {{ expediente.horas?.start }}–{{ expediente.horas?.end }}
        </span>
        · a faixa hachurada é depois do fechamento, onde só a última mesa da noite
        pode terminar.
      </p>

      <!-- A lista NÃO é a grade em formato de tabela — ela responde outra pergunta.
           A grade organiza por MESA ("o que está livre às 20h?"); a lista organiza
           por HORÁRIO ("quem chega primeiro, e qual o telefone?"). São as duas
           perguntas que o staff faz, e nenhuma das duas telas responde a outra bem. -->
      <div class="mt-12">
        <ListaReservas
          :reservas="agenda.reservas"
          :disponibilidade="agenda.disponibilidade"
          :tz="expediente.tz"
          @abrir="reservaAberta = $event"
        />
      </div>
    </template>

    <ReservaDialog
      :aberto="dialogoAberto"
      :dia="agenda.dia"
      :tz="expediente.tz"
      :disponibilidade="agenda.disponibilidade"
      :mesa-pre="mesaPre"
      :minutos-pre="minutosPre"
      :reserva-pre="reservaPre"
      @fechar="dialogoAberto = false"
      @salvo="dialogoAberto = false"
    />

    <ReservaDetalhe
      :reserva="reservaAberta"
      :disponibilidade="agenda.disponibilidade"
      :tz="expediente.tz"
      @editar="abrirEdicao"
      @fechar="reservaAberta = null"
    />
  </div>
</template>

<style scoped>
.barra-carga {
  animation: deslize 1.1s ease-in-out infinite;
}

@keyframes deslize {
  0% {
    transform: translateX(-100%);
  }
  100% {
    transform: translateX(400%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .barra-carga {
    animation: none;
  }
}
</style>
