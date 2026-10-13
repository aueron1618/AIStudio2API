<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from '@/i18n'

const accountLocale = defineModel<string>('locale', { required: true })
const accountTimezone = defineModel<string>('timezone', { required: true })
const { t } = useI18n()
const localeListID = useId()
const timezoneListID = useId()
</script>

<template>
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
    <label class="block">
      <span class="mb-1 block text-sm font-medium text-gray-400">{{ t('accounts.locale') }}</span>
      <input
        v-model.trim="accountLocale"
        :list="localeListID"
        class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
        required
      />
      <datalist :id="localeListID">
        <option value="zh-CN" :label="t('accounts.localeZhCN')" />
        <option value="en-US" :label="t('accounts.localeEnUS')" />
      </datalist>
    </label>
    <label class="block">
      <span class="mb-1 block text-sm font-medium text-gray-400">{{ t('accounts.timezone') }}</span>
      <input
        v-model.trim="accountTimezone"
        :list="timezoneListID"
        class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
        required
      />
      <datalist :id="timezoneListID">
        <option value="Asia/Shanghai" :label="t('accounts.timezoneShanghai')" />
        <option value="America/New_York" :label="t('accounts.timezoneNewYork')" />
      </datalist>
    </label>
  </div>
</template>
