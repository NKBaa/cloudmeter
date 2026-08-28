import { ref } from "vue";
import { api } from "./api";

export interface SystemSettings {
  systemName: string;
  updatedAt?: string;
}

const systemName = ref<string>(localStorage.getItem("cloudmeter_system_name") || "CloudMeter");

export function useSiteConfig() {
  async function fetchSiteConfig() {
    try {
      const res = await api<SystemSettings>("/system/settings");
      if (res && res.systemName) {
        systemName.value = res.systemName;
        localStorage.setItem("cloudmeter_system_name", res.systemName);
        document.title = res.systemName;
      }
    } catch {
      // Fallback
    }
  }

  function setSystemName(name: string) {
    if (!name) return;
    systemName.value = name;
    localStorage.setItem("cloudmeter_system_name", name);
    document.title = name;
  }

  return {
    systemName,
    fetchSiteConfig,
    setSystemName,
  };
}
