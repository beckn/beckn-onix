"use client";
import SecondaryButton from "@/components/Buttons/SecondaryButton";
import styles from "../../page.module.css";
import { Ubuntu_Mono } from "next/font/google";
import Slider from "@/components/Slider/Slider";
import PrimaryButton from "@/components/Buttons/PrimaryButton";
import InputField from "@/components/InputField/InputField";
import { useState, useCallback } from "react";
import { toast } from "react-toastify";

const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function InstallYaml() {
  const [yamlUrl, setYamlUrl] = useState("");
  const [checked, setChecked] = useState(false);

  const container = checked ? "bpp" : "bap";

  const handleRegistryUrlChange = (event) => {
    setYamlUrl(event.target.value);
  };

  const installYaml = useCallback(async () => {
    const toastId = toast.loading("Installing Layer 2 Config file...");

    try {
      const response = await fetch("/api/install-layer2", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ container, yamlUrl }),
      });

      if (response.ok) {
        const data = await response.json();
        console.log(data);
        const FileFound = data.message;
        if (FileFound == false) {
          setShowDownloadLayer2Button(true);
          toast.update(toastId, {
            render: "No Layer 2 Config Present 🤯",
            type: "error",
            isLoading: false,
            autoClose: 5000,
          });
        } else {
          toast.update(toastId, {
            render: "Yaml File Downloaded 👌",
            type: "success",
            isLoading: false,
            autoClose: 5000,
          });
        }
      } else {
        console.error("Failed to check yaml");
        toast.update(toastId, {
          render: "Container Not Found 🤯",
          type: "error",
          isLoading: false,
          autoClose: 5000,
        });
      }
    } catch (error) {
      console.error("An error occurred:", error);
    }
  });

  return (
    <>
      <main className={ubuntuMono.className}>
        <div className={styles.mainContainer}>
          <button
            onClick={() => window.history.back()}
            className={styles.backButton}
          >
            Back
          </button>
          <p className={styles.mainText}>Install Yaml</p>
          <div className={styles.formContainer}>
            <Slider
              label={checked ? "BPP" : "BAP"}
              checked={checked}
              toggleChecked={setChecked}
            />
            <InputField
              label={"Enter Layer 2 URL"}
              value={yamlUrl}
              onChange={handleRegistryUrlChange}
              placeholder="https://github/user/repo/blob/main/layer2.yaml"
            />
            <div className={styles.buttonsContainer}>
              {/* <SecondaryButton text={"Cancel"} /> */}
              <PrimaryButton onClick={installYaml} text={"Continue"} />
            </div>
          </div>
        </div>
      </main>
    </>
  );
}
