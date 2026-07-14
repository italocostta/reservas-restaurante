<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { api } from '@/api/client'
import { mensagemLegivel } from '@/api/errors'
import MesaDialog from '@/components/MesaDialog.vue'
import MesaLinha from '@/components/MesaLinha.vue'
import { hojeNoRestaurante } from '@/lib/tempo'
import { statusMesa, type StatusMesa } from '@/lib/statusMesa'
import { useExpedienteStore } from '@/stores/expediente'
import { useTablesStore, type FiltroMesas } from '@/stores/tables'
import type { Reservation, Table } from '@/types/api'

const store = useTablesStore()
const expediente = useExpedienteStore()

// As reservas de HOJE, para o status operacional de cada mesa. O Salão é sempre
// sobre hoje — "ocupada agora" não faz sentido para outro dia.
const reservasHoje = ref<Reservation[]>([])

/**
 * O status de uma mesa é recalculado a cada render (Date.now() muda), e isso é de
 * propósito: uma reserva que começa às 20h faz a mesa virar "ocupada" às 20h sem
 * ninguém recarregar. Sem estado congelado, sem timer — o Vue reavalia quando algo
 * o invalida, e o pior caso é o status atrasar até a próxima interação. Aceitável:
 * o status é uma dica de relance, não um relógio.
 */
function status(mesa: Table): StatusMesa {
  return statusMesa(reservasHoje.value, mesa.id, mesa.is_active, Date.now())
}

async function carregarStatus() {
  await expediente.carregar()
  if (!expediente.pronto) return
  try {
    reservasHoje.value = await api.reservations.list({
      date: hojeNoRestaurante(expediente.tz),
      status: 'confirmed',
    })
  } catch {
    // Status é enfeite informativo: se falhar, a lista de mesas continua útil.
    // Não sobrescrevo o erroDeCarga do store por causa de uma dica de status.
    reservasHoje.value = []
  }
}

const dialogoAberto = ref(false)
const emEdicao = ref<Table | null>(null)

/** O id da mesa que acabou de mudar — usado para o pulso de confirmação. */
const recemMudada = ref<string | null>(null)

/** Erro de uma AÇÃO na lista (desativar/reativar). O de carga vem do store. */
const erroDeAcao = ref<string | null>(null)

const filtros: { valor: FiltroMesas; rotulo: string }[] = [
  { valor: 'todas', rotulo: 'Todas' },
  { valor: 'ativas', rotulo: 'De pé' },
  { valor: 'inativas', rotulo: 'Fora' },
]

onMounted(() => {
  store.carregar()
  carregarStatus()
})

function abrirNova() {
  emEdicao.value = null
  dialogoAberto.value = true
}

function abrirEdicao(mesa: Table) {
  emEdicao.value = mesa
  dialogoAberto.value = true
}

async function alternar(mesa: Table) {
  erroDeAcao.value = null
  try {
    const atualizada = await store.alternarAtiva(mesa)
    pulsar(atualizada.id)
  } catch (e) {
    erroDeAcao.value = mensagemLegivel(e)
  }
}

/**
 * O ÚNICO movimento gratuito da interface — e ele não é gratuito.
 *
 * Desativar uma mesa só muda a opacidade da linha e apaga um talho verde de 2px.
 * Numa lista de 30 linhas, isso é perfeitamente possível de não ver — e "não ter
 * certeza se o clique pegou" é o que faz o staff clicar de novo. O pulso existe
 * para responder "sim, foi essa linha", que é informação, não enfeite.
 */
function pulsar(id: string) {
  recemMudada.value = id
  setTimeout(() => {
    if (recemMudada.value === id) recemMudada.value = null
  }, 900)
}
</script>

<template>
  <div>
    <!-- Cabeçalho como PLACA: os três números que descrevem o salão, em mono, com
         a mesma hierarquia. Não são "KPI cards" — são um instrumento de painel. -->
    <div class="mb-8 flex flex-wrap items-end justify-between gap-6">
      <div class="flex items-end gap-8">
        <div>
          <p class="rotulo mb-1">Mesas</p>
          <p class="dado text-ink-100 text-4xl leading-none font-medium">
            {{ store.mesas.length }}
          </p>
        </div>

        <div class="bg-ink-800 h-10 w-px"></div>

        <div>
          <p class="rotulo mb-1">De pé</p>
          <p class="dado text-sage-500 text-4xl leading-none font-medium">
            {{ store.totalAtivas }}
          </p>
        </div>

        <div class="bg-ink-800 h-10 w-px"></div>

        <div>
          <p class="rotulo mb-1">Lugares</p>
          <p class="dado text-ember-400 text-4xl leading-none font-medium">
            {{ store.lugaresAtivos }}
          </p>
          <p class="text-ink-500 mt-1 text-[0.6875rem]">só as mesas de pé</p>
        </div>
      </div>

      <button
        type="button"
        class="font-display bg-ember-500 text-ink-950 hover:bg-ember-400 px-5 py-2.5 text-sm font-bold tracking-wide uppercase transition-colors"
        @click="abrirNova"
      >
        + Nova mesa
      </button>
    </div>

    <!-- Filtro: três abas, marcador de brasa embaixo da ativa. O filtro é local
         (o store lista tudo uma vez) — trocar de aba não pode custar round-trip
         nem piscar um "carregando". -->
    <div class="border-ink-800 mb-1 flex items-center gap-1 border-b">
      <button
        v-for="f in filtros"
        :key="f.valor"
        type="button"
        class="font-display relative px-4 py-2.5 text-xs font-semibold tracking-[0.1em] uppercase transition-colors"
        :class="store.filtro === f.valor ? 'text-ink-100' : 'text-ink-500 hover:text-ink-300'"
        @click="store.filtro = f.valor"
      >
        {{ f.rotulo }}
        <span
          v-if="store.filtro === f.valor"
          class="bg-ember-500 absolute inset-x-4 -bottom-px h-0.5"
        ></span>
      </button>
    </div>

    <p
      v-if="erroDeAcao"
      class="border-blood-500/40 bg-blood-500/10 text-blood-300 mt-4 border-l-2 px-3 py-2 text-sm"
      role="alert"
    >
      {{ erroDeAcao }}
    </p>

    <!-- Carregando: barra de brasa deslizando, não um spinner nem esqueletos
         cinzas. É a linguagem do resto da tela. -->
    <div v-if="store.carregando" class="bg-ink-800 mt-6 h-0.5 overflow-hidden">
      <div class="bg-ember-500 h-full w-1/3 barra-carga"></div>
    </div>

    <p
      v-else-if="store.erroDeCarga"
      class="border-blood-500/40 bg-blood-500/10 text-blood-300 mt-6 border-l-2 px-4 py-3 text-sm"
      role="alert"
    >
      {{ store.erroDeCarga }}
    </p>

    <!-- Vazio: dois estados DIFERENTES, e confundi-los seria mentir. "Nenhuma mesa
         cadastrada" pede uma ação; "nenhuma mesa neste filtro" pede trocar de aba.
         Um único texto genérico mandaria o staff cadastrar uma mesa que já existe. -->
    <div v-else-if="store.visiveis.length === 0" class="py-16 text-center">
      <template v-if="store.mesas.length === 0">
        <p class="font-display text-ink-300 text-lg font-semibold">O salão está vazio.</p>
        <p class="text-ink-500 mt-1 text-sm">Cadastre a primeira mesa para começar.</p>
      </template>
      <template v-else>
        <p class="font-display text-ink-400 text-base font-semibold">
          Nenhuma mesa {{ store.filtro === 'ativas' ? 'de pé' : 'fora de operação' }}.
        </p>
      </template>
    </div>

    <ul v-else class="mt-1">
      <MesaLinha
        v-for="mesa in store.visiveis"
        :key="mesa.id"
        :mesa="mesa"
        :status="status(mesa)"
        :class="{ pulso: recemMudada === mesa.id }"
        @editar="abrirEdicao(mesa)"
        @alternar="alternar(mesa)"
      />
    </ul>

    <MesaDialog
      :mesa="emEdicao"
      :aberto="dialogoAberto"
      @fechar="dialogoAberto = false"
      @salvo="pulsar($event.id)"
    />
  </div>
</template>

<style scoped>
/* Classes NOMEADAS, e não utilitárias de valor arbitrário (`animate-[pulso_0.9s
   _ease-out]`).

   A versão anterior fazia o @media do prefers-reduced-motion casar com a string
   literal da classe do Tailwind — `.animate-\[pulso_0\.9s_ease-out\]`. Isso
   significa que trocar 0.9s por 0.7s quebraria o seletor EM SILÊNCIO, e quem
   desligou animação no sistema voltaria a ver o pulso. Um seletor que depende do
   valor da propriedade que ele quer desligar é uma bomba-relógio. */

.barra-carga {
  animation: deslize 1.1s ease-in-out infinite;
}

/* O pulso é uma lambida de brasa que atravessa a linha e some. Curto de propósito:
   confirma e sai do caminho — quem está no passe não espera a animação acabar para
   clicar na próxima. */
.pulso {
  animation: pulso 0.9s ease-out;
}

@keyframes deslize {
  0% {
    transform: translateX(-100%);
  }
  100% {
    transform: translateX(400%);
  }
}

@keyframes pulso {
  0% {
    background-color: rgb(242 105 27 / 0.18);
  }
  100% {
    background-color: transparent;
  }
}

/* O pulso é informação — mas informação que TAMBÉM existe no estado final da linha
   (opacidade, talho verde apagado). Desligá-lo não esconde nada de quem pediu
   menos movimento. */
@media (prefers-reduced-motion: reduce) {
  .barra-carga,
  .pulso {
    animation: none;
  }
}
</style>
