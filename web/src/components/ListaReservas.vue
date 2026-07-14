<script setup lang="ts">
import { computed } from 'vue'

import { formatarTelefone } from '@/lib/telefone'
import { horaLocal } from '@/lib/tempo'
import type { Reservation, TableAvailability } from '@/types/api'

const props = defineProps<{
  reservas: Reservation[]
  disponibilidade: TableAvailability[]
  tz: string
}>()

defineEmits<{ abrir: [reserva: Reservation] }>()

/**
 * Por horário de entrada. A grade já organiza por MESA — esta lista existe
 * justamente para a outra pergunta: "quem chega primeiro?".
 *
 * Duplicar a ordenação da grade aqui seria construir a mesma tela duas vezes.
 */
const ordenadas = computed(() =>
  [...props.reservas].sort((a, b) => a.starts_at.localeCompare(b.starts_at)),
)

function nomesDasMesas(r: Reservation): string {
  return r.table_ids
    .map((id) => props.disponibilidade.find((m) => m.table_id === id)?.table_name ?? '—')
    .join(' + ')
}
</script>

<template>
  <div>
    <div class="mb-3 flex items-baseline gap-3">
      <h2 class="rotulo">Chegadas</h2>
      <span class="bg-ink-800 h-px flex-1"></span>
    </div>

    <p v-if="ordenadas.length === 0" class="text-ink-600 py-8 text-center text-sm">
      Nenhuma reserva para este dia.
    </p>

    <table v-else class="w-full border-collapse text-sm">
      <thead>
        <tr class="border-ink-800 border-b">
          <th class="rotulo py-2 pr-4 text-left font-normal">Entra</th>
          <th class="rotulo py-2 pr-4 text-left font-normal">Cliente</th>
          <th class="rotulo py-2 pr-4 text-left font-normal">Telefone</th>
          <th class="rotulo py-2 pr-4 text-right font-normal">Pessoas</th>
          <th class="rotulo py-2 text-left font-normal">Mesa</th>
        </tr>
      </thead>

      <tbody>
        <tr
          v-for="r in ordenadas"
          :key="r.id"
          class="border-ink-850 hover:bg-ink-900 cursor-pointer border-b transition-colors"
          @click="$emit('abrir', r)"
        >
          <!-- A hora é o dado que se lê em coluna, de cima a baixo, para saber quem
               vem primeiro. Mono e tabular: os dígitos alinham sozinhos. -->
          <td class="dado text-ink-100 py-2.5 pr-4 whitespace-nowrap">
            {{ horaLocal(r.starts_at, tz) }}
            <span class="text-ink-600">–{{ horaLocal(r.ends_at, tz) }}</span>
          </td>

          <td class="font-display text-ink-100 py-2.5 pr-4 font-semibold">
            {{ r.customer_name }}
          </td>

          <!-- Telefone é LINK: esta lista existe para ligar confirmando, e o staff
               está com o celular na mão. Um telefone que só se pode ler é metade de
               um telefone. -->
          <td class="py-2.5 pr-4">
            <a
              :href="`tel:${r.customer_phone}`"
              class="dado text-ink-300 hover:text-ember-400 transition-colors"
              @click.stop
            >
              {{ formatarTelefone(r.customer_phone) }}
            </a>
          </td>

          <td class="dado text-ink-100 py-2.5 pr-4 text-right">{{ r.party_size }}</td>

          <td class="py-2.5">
            <span class="font-display text-ink-300 text-sm">{{ nomesDasMesas(r) }}</span>
            <!-- A combinação é marcada aqui também. Ver "Mesa 06 + Mesa 04" sem
                 saber que é uma junção deliberada faria parecer erro de dado. -->
            <span
              v-if="r.table_ids.length > 1"
              class="rotulo text-ember-400 ml-2"
              title="Mesas combinadas — a junção foi feita por uma pessoa no salão"
            >
              ⛓ Combinada
            </span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
