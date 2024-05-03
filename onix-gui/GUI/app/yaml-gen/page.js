"use client";
import { useState } from "react";
import { Ubuntu_Mono } from "next/font/google";
import PrimaryButton from "@/components/Buttons/PrimaryButton";
import InputField from "@/components/InputField/InputField";
import Slider from "@/components/Slider/Slider";
import styles from "../page.module.css";
import { toast } from "react-toastify";

import Link from "next/link";

const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function CheckYaml() {
  const [domainName, setDomainName] = useState("");
  const [versionNumber, setversionNumber] = useState("");
  const [checked, setChecked] = useState(false);
  const [showDownloadLayer2Button, setShowDownloadLayer2Button] =
    useState(false);
  const [propertyLink, setPropertyLink] = useState("");

  const handleYamlChange = (event) => {
    setPropertyLink(event.target.value);
  };
  const handledomainNameChange = (event) => {
    setDomainName(event.target.value);
  };
  const handleVersionChange = (event) => {
    setversionNumber(event.target.value);
  };
  const nameGenerator = async () => {
    const parts = domainName.split(":");
    const domainNameWithoutVersion = parts[0];
    let filename;
    if (parts[1] === undefined || parts[1] === "") {
      filename = `${domainNameWithoutVersion}_${versionNumber}.yaml`;
    } else {
      filename = `${domainNameWithoutVersion}_${parts[1]}_${versionNumber}.yaml`;
    }
    console.log(filename);
    return filename;
  };

  const handleOnclick = async () => {
    const fileName = await nameGenerator();
    const toastId = toast.loading("Checking for layer2 yaml file");
    try {
      const response = await fetch("/api/check-layer2", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ checked, fileName }),
      });

      if (response.ok) {
        const data = await response.json();
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
            render: "Yaml File Present 👌",
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
  };
  return (
    <>
      <main className={ubuntuMono.className}>
        <button
          onClick={() => window.history.back()}
          className={styles.backButton}
        >
          Back
        </button>

        <div className={styles.mainContainer}>
          <p className={styles.mainText}>
            <b>Yaml File Checker</b>
          </p>

          <div className={styles.formContainer}>
            <Slider
              label={checked ? "BPP" : "BAP"}
              checked={checked}
              toggleChecked={setChecked}
            />
            <InputField
              label={"Container Name"}
              value={checked ? "bpp-network" : "bap-network"}
              readOnly
            />
            <InputField
              label={"Domain Name"}
              value={domainName}
              onChange={handledomainNameChange}
              placeholder="Retail"
            />
            <InputField
              label={"Version Number"}
              value={versionNumber}
              onChange={handleVersionChange}
              placeholder="1.0.0"
            />

            <div className={styles.buttonsContainer}>
              <PrimaryButton label={"Check"} onClick={handleOnclick} />
            </div>
            {showDownloadLayer2Button && (
              <div className={styles.buttonsContainer}>
                <a href={`/yaml-gen/install-yaml`}>Download Layer 2 Config</a>
              </div>
            )}
          </div>
        </div>
      </main>
    </>
  );
}
