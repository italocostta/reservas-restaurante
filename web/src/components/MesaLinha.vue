<script setup lang="ts">
import { APARENCIA_STATUS, type StatusMesa } from '@/lib/statusMesa'
import type { Table } from '@/types/api'

const props = defineProps<{ mesa: Table; status: StatusMesa }>()
defineEmits<{ editar: []; alternar: [] }>()

// O talho vertical de estado agora reflete o STATUS de hoje, não só ativa/inativa:
// sage = disponível, brasa = reservada, sangue = ocupada, apagado = fora. É a
// primeira coisa que o olho pega ao correr a lista — cor antes de palavra.
const corDoTalho: Record<StatusMesa, string> = {
  disponivel: 'bg-sage-500',
  reservada: 'bg-ember-500',
  ocupada: 'bg-blood-500',
  inativa: 'bg-transparent',
}
const talho = () => corDoTalho[props.status]
const aparencia = () => APARENCIA_STATUS[props.status]
</script>

<template>
  <!-- Linha, não card. Um grid de cards com sombra e canto arredondado é o
       vocabulário de todo dashboard genérico e desperdiça a densidade que o passe
       exige: numa linha, o olho corre a coluna de capacidade sem saltar entre
       caixas. -->
  <li
    class="group border-ink-800 hover:bg-ink-900 relative flex items-center gap-5 border-b px-4 py-3.5 transition-colors"
    :class="{ 'opacity-45': !mesa.is_active }"
  >
    <!-- Talho vertical de estado: a cor É o status. -->
    <span
      class="absolute inset-y-0 left-0 w-0.5 transition-colors"
      :class="talho()"
      aria-hidden="true"
    ></span>

    <!-- A capacidade é o dado que o staff busca, então ela é o maior número da
         linha — mono, tabular, alinhada. Não um badge, não um ícone de cadeira. -->
    <div class="flex w-14 shrink-0 items-baseline gap-1">
      <span class="dado text-ink-100 text-2xl leading-none font-medium">
        {{ mesa.capacity }}
      </span>
      <span class="text-ink-500 text-[0.625rem] tracking-wide uppercase">lug</span>
    </div>

    <div class="min-w-0 flex-1">
      <p class="font-display text-ink-100 truncate text-base font-semibold">
        {{ mesa.name }}
      </p>
      <p class="rotulo mt-0.5" :class="aparencia().texto">{{ aparencia().rotulo }}</p>
    </div>

    <!-- As ações só aparecem no hover/foco. Numa lista de 30 mesas, 60 botões
         permanentes competem com o dado — e o dado é o que a pessoa veio ler.
         focus-within mantém isso navegável no teclado, que é como o staff com
         pressa opera. -->
    <div
      class="flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100"
    >
      <button
        type="button"
        class="font-display text-ink-400 hover:text-ember-400 px-3 py-1.5 text-xs font-semibold tracking-wide uppercase transition-colors"
        @click="$emit('editar')"
      >
        Editar
      </button>

      <button
        type="button"
        class="font-display px-3 py-1.5 text-xs font-semibold tracking-wide uppercase transition-colors"
        :class="
          mesa.is_active
            ? 'text-ink-400 hover:text-blood-300'
            : 'text-ink-400 hover:text-sage-300'
        "
        @click="$emit('alternar')"
      >
        {{ mesa.is_active ? 'Desativar' : 'Reativar' }}
      </button>
    </div>
  </li>
</template>
