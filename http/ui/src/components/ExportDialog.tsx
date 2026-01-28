import { useMemo, useState } from "react";
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  IconButton,
  Snackbar,
  Switch,
  Tab,
  Tabs,
  TextField,
  Typography,
} from "@mui/material";
import ContentCopyIcon from "@mui/icons-material/ContentCopy";
import type { ScanResult } from "../types";
import { getRootDomain, normalizeDomain, sortIPs } from "../utils/domain";

interface ExportDialogProps {
  open: boolean;
  onClose: () => void;
  result: ScanResult;
}

export function ExportDialog({
  open,
  onClose,
  result,
}: Readonly<ExportDialogProps>) {
  const [tab, setTab] = useState(0);
  const [rootDomainsOnly, setRootDomainsOnly] = useState(false);
  const [copySnackbar, setCopySnackbar] = useState(false);

  const { domainsList, domainsCount, ipsList, ipsCount } = useMemo(() => {
    const domainData = result.domains || [];
    let processedDomains = domainData.map((d) => normalizeDomain(d.domain));

    if (rootDomainsOnly) {
      processedDomains = processedDomains.map(getRootDomain);
    }

    const uniqueDomains = [...new Set(processedDomains)].sort((a, b) =>
      a.localeCompare(b),
    );
    const domains = uniqueDomains.map((d) => `domain:${d}`).join("\n");

    const uniqueIPs = [
      ...new Set(domainData.flatMap((d) => d.ips || []).filter(Boolean)),
    ];
    const ips = sortIPs(uniqueIPs).join("\n");

    return {
      domainsList: domains,
      domainsCount: uniqueDomains.length,
      ipsList: ips,
      ipsCount: uniqueIPs.length,
    };
  }, [result, rootDomainsOnly]);

  const handleCopy = async (text: string) => {
    await navigator.clipboard.writeText(text);
    setCopySnackbar(true);
  };

  return (
    <>
      <Dialog open={open} onClose={onClose} maxWidth="md" fullWidth>
        <DialogTitle>Export</DialogTitle>
        <DialogContent>
          <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mb: 2 }}>
            <Tab label={`Domains (${domainsCount})`} />
            <Tab label={`IPs (${ipsCount})`} />
          </Tabs>

          {tab === 0 && (
            <Box>
              <Box
                sx={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  mb: 1,
                }}
              >
                <FormControlLabel
                  control={
                    <Switch
                      checked={rootDomainsOnly}
                      onChange={(e) => setRootDomainsOnly(e.target.checked)}
                      size="small"
                    />
                  }
                  label={
                    <Typography variant="body2">Root domains only</Typography>
                  }
                />
                <IconButton
                  size="small"
                  onClick={() => handleCopy(domainsList)}
                  title="Copy"
                >
                  <ContentCopyIcon fontSize="small" />
                </IconButton>
              </Box>
              <TextField
                fullWidth
                multiline
                rows={15}
                value={domainsList}
                InputProps={{
                  readOnly: true,
                  sx: { fontFamily: "monospace", fontSize: "0.8rem" },
                }}
              />
            </Box>
          )}

          {tab === 1 && (
            <Box>
              <Box
                sx={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  mb: 1,
                }}
              >
                <Typography variant="body2" color="text.secondary">
                  IPs
                </Typography>
                <IconButton
                  size="small"
                  onClick={() => handleCopy(ipsList)}
                  title="Copy"
                >
                  <ContentCopyIcon fontSize="small" />
                </IconButton>
              </Box>
              <TextField
                fullWidth
                multiline
                rows={15}
                value={ipsList}
                InputProps={{
                  readOnly: true,
                  sx: { fontFamily: "monospace", fontSize: "0.8rem" },
                }}
              />
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={onClose}>Close</Button>
        </DialogActions>
      </Dialog>

      <Snackbar
        open={copySnackbar}
        autoHideDuration={2000}
        onClose={() => setCopySnackbar(false)}
        message="Copied to clipboard"
      />
    </>
  );
}
