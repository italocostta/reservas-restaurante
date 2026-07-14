<script setup lang="ts">
import { ref, watch } from 'vue'

import { mensagemLegivel } from '@/api/errors'
import { formatarTelefone } from '@/lib/telefone'
import { horaLocal } from '@/lib/tempo'
import { useAgendaStore } from '@/stores/agenda'
import type { Reservation, TableAvailability } from '@/types/api'

const props = defineProps<{
  reserva: Reservation | null
  disponibilidade: TableAvailability[]
  tz: string
}>()

const emit = defineEmits<{ fechar: [] }>()

const agenda = useAgendaStore()

const dialogo = ref<HTMLDialogElement>()
const confirmando = ref(false)
const cancelando = ref(false)
const erro = ref<string | null>(null)

watch(
  () => props.reserva,
  (r) => {
    erro.value = null
    confirmando.value = false
    if (r) dialogo.value?.showModal()
    else dialogo.value?.close()
  },
)

/** Os nomes das mesas, a partir dos ids que a reserva carrega. */
function nomesDasMesas(r: Reservation): string {
  return r.table_ids
    .map((id) => props.disponibilidade.find((m) => m.table_id === id)?.table_name ?? '—')
    .join(' + ')
}

async function cancelar() {
  if (!props.reserva) return

  cancelando.value = true
  erro.value = null

  try {
    await agenda.cancelar(props.reserva.id)
    emit('fechar')
  } catch (e) {
    erro.value = mensagemLegivel(e)
  } finally {
    cancelando.value = false
  }
}
</script>

<template>
  <dialog
    ref="dialogo"
    class="bg-ink-900 border-ink-700 text-ink-100 m-auto w-[24rem] max-w-[calc(100vw-2rem)] border p-0 shadow-2xl backdrop:bg-black/70"
    @close="emit('fechar')"
    @cancel="emit('fechar')"
  >
    <div v-if="reserva">
      <div class="bg-ember-500 h-1"></div>

      <div class="px-6 py-5">
        <p class="font-display text-xl font-bold">{{ reserva.customer_name }}</p>
        <a
          :href="`tel:${reserva.customer_phone}`"
          class="dado text-ink-400 hover:text-ember-400 mt-0.5 block text-sm transition-colors"
        >
          {{ formatarTelefone(reserva.customer_phone) }}
        </a>

        <dl class="border-ink-800 mt-5 space-y-3 border-t pt-5">
          <div class="flex justify-between">
            <dt class="rotulo">Horário</dt>
            <dd class="dado text-ink-100 text-sm">
              {{ horaLocal(reserva.starts_at, tz) }} – {{ horaLocal(reserva.ends_at, tz) }}
            </dd>
          </div>
          <div class="flex justify-between">
            <dt class="rotulo">Pessoas</dt>
            <dd class="dado text-ink-100 text-sm">{{ reserva.party_size }}</dd>
          </div>
          <div class="flex justify-between gap-4">
            <dt class="rotulo shrink-0">
              {{ reserva.table_ids.length > 1 ? 'Combinação' : 'Mesa' }}
            </dt>
            <dd class="font-display text-ink-100 text-right text-sm font-semibold">
              {{ nomesDasMesas(reserva) }}
            </dd>
          </div>
        </dl>

        <p
          v-if="erro"
          class="border-blood-500/40 bg-blood-500/10 text-blood-300 mt-4 border-l-2 px-3 py-2 text-sm"
          role="alert"
        >
          {{ erro }}
        </p>
      </div>

      <footer class="border-ink-700 bg-ink-850 flex items-center justify-between border-t px-6 py-4">
        <!-- O cancelamento pede confirmação em DOIS passos, e não é cerimônia.
             Cancelar é soft delete: a reserva sai da grade, o horário é liberado
             para o próximo, e — desde a Fase 2 — um evento de notificação é
             enfileirado. O cliente RECEBE um aviso de cancelamento. Um clique
             errado não manda só uma linha para o banco: ele liga para alguém. -->
        <template v-if="!confirmando">
          <button
            type="button"
            class="font-display text-ink-500 hover:text-blood-300 text-xs font-semibold tracking-wide uppercase transition-colors"
            @click="confirmando = true"
          >
            Cancelar reserva
          </button>
          <button
            type="button"
            class="font-display text-ink-300 hover:text-ink-100 px-4 py-2 text-sm font-semibold tracking-wide uppercase"
            @click="emit('fechar')"
          >
            Fechar
          </button>
        </template>

        <template v-else>
          <span class="text-ink-300 text-xs">Cancelar e avisar o cliente?</span>
          <div class="flex gap-2">
            <button
              type="button"
              class="font-display text-ink-400 hover:text-ink-100 px-3 py-2 text-xs font-semibold tracking-wide uppercase"
              @click="confirmando = false"
            >
              Não
            </button>
            <button
              type="button"
              :disabled="cancelando"
              class="font-display bg-blood-500 text-ink-100 px-4 py-2 text-xs font-bold tracking-wide uppercase transition-opacity hover:opacity-90 disabled:opacity-50"
              @click="cancelar"
            >
              {{ cancelando ? 'Cancelando…' : 'Sim, cancelar' }}
            </button>
          </div>
        </template>
      </footer>
    </div>
  </dialog>
</template>
