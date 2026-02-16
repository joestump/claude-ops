import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api/claude-ops-api",
    },
    {
      type: "category",
      label: "UNTAGGED",
      items: [
        {
          type: "doc",
          id: "api/get-health",
          label: "Health check",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/list-sessions",
          label: "List sessions",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/get-session",
          label: "Get session detail",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/trigger-session",
          label: "Trigger ad-hoc session",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api/list-events",
          label: "List events",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/list-memories",
          label: "List memories",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/create-memory",
          label: "Create memory",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api/update-memory",
          label: "Update memory",
          className: "api-method put",
        },
        {
          type: "doc",
          id: "api/delete-memory",
          label: "Delete memory",
          className: "api-method delete",
        },
        {
          type: "doc",
          id: "api/list-cooldowns",
          label: "List cooldowns",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/get-config",
          label: "Get configuration",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/update-config",
          label: "Update configuration",
          className: "api-method put",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
